package elevate

import (
	"errors"
	"fmt"
	"io/fs"
	"strings"
	"testing"
)

// TestInstallScriptQuoting 验证 AppleScript/shell 两级转义与三段式
// 脚本结构：cat 到同目录暂存文件 → chmod（保留原权限位）→ mv 原子
// 覆盖 → 落位成功后才刷新 DNS 且刷新失败不影响结果。
func TestInstallScriptQuoting(t *testing.T) {
	tests := []struct {
		name   string
		src    string
		target string
		mode   fs.FileMode
		want   []string // 脚本中必须包含的子串
	}{
		{
			name:   "plain paths",
			src:    "/var/folders/ab/.mhost-hosts-1",
			target: "/etc/hosts",
			mode:   0o644,
			want: []string{
				`do shell script `,
				`with administrator privileges`,
				`/bin/cat '/var/folders/ab/.mhost-hosts-1' > '/etc/hosts.mhost-new'`,
				`/bin/chmod 644 '/etc/hosts.mhost-new'`,
				`/bin/mv -f '/etc/hosts.mhost-new' '/etc/hosts'`,
				// 刷新只在落位成功后执行，且失败不影响退出码
				"/usr/bin/dscacheutil -flushcache || true",
				"/usr/bin/killall -HUP mDNSResponder || true",
				"exit 0",
				// 主分支失败后降级：先确认源可读再截断写既有文件
				"elif /bin/test -r '/var/folders/ab/.mhost-hosts-1' && /bin/cat '/var/folders/ab/.mhost-hosts-1' > '/etc/hosts'; then",
				// 失败路径清理暂存文件并以非零退出
				`/bin/rm -f '/etc/hosts.mhost-new'`,
				"exit 1",
			},
		},
		{
			name:   "mode rendered as octal",
			src:    "/tmp/staged",
			target: "/etc/hosts",
			mode:   0o600,
			want:   []string{`/bin/chmod 600 '/etc/hosts.mhost-new'`},
		},
		{
			name:   "spaces are quoted",
			src:    "/tmp/my dir/staged hosts",
			target: "/etc/hosts",
			mode:   0o644,
			want:   []string{`'/tmp/my dir/staged hosts'`},
		},
		{
			name:   "single quote escaped for shell",
			src:    "/tmp/it's",
			target: "/etc/hosts",
			mode:   0o644,
			want:   []string{`'/tmp/it'\\''s'`},
		},
		{
			name:   "backslash escaped for applescript",
			src:    `/tmp/back\slash`,
			target: "/etc/hosts",
			mode:   0o644,
			want:   []string{`back\\slash`},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			script := InstallScript(tt.src, tt.target, tt.mode)
			for _, want := range tt.want {
				if !strings.Contains(script, want) {
					t.Errorf("script missing %q:\n%s", want, script)
				}
			}
			// 结构不变量：mv 必须在刷新之前（刷新不能阻塞或截断落位），
			// 且主/降级两条成功分支各带一次刷新
			mv := strings.Index(script, "/bin/mv -f")
			flush := strings.Index(script, "/usr/bin/dscacheutil")
			if mv < 0 || flush < 0 || flush < mv {
				t.Errorf("flush must come after mv:\n%s", script)
			}
			if got := strings.Count(script, "dscacheutil -flushcache"); got != 2 {
				t.Errorf("flush blocks = %d, want 2 (primary + fallback):\n%s", got, script)
			}
		})
	}
}

// TestInstallWithClassification 验证错误分类：用户取消（-128 /
// "User canceled"）与授权失败（-60005 / "authorization failed"）必须
// 各自归类哨兵，服务层据此给出明确提示；其余原样返回。
func TestInstallWithClassification(t *testing.T) {
	tests := []struct {
		name    string
		output  string
		runErr  error
		wantErr error // nil 表示期待非哨兵的普通错误/成功
	}{
		{name: "success", output: "", runErr: nil, wantErr: nil},
		{name: "user canceled en", output: "errAEUserCanceledNumber -128 User canceled.", runErr: errors.New("exit status 1"), wantErr: ErrCanceled},
		{name: "user canceled number only", output: "error number -128", runErr: errors.New("exit status 1"), wantErr: ErrCanceled},
		{name: "auth failed wrong password", output: "execution error: Administrator authorization failed. (-60005)", runErr: errors.New("exit status 1"), wantErr: ErrAuthFailed},
		{name: "auth failed wording", output: "authorization failed for unknown reason", runErr: errors.New("exit status 1"), wantErr: ErrAuthFailed},
		{name: "generic failure", output: "cat: /etc/hosts.mhost-new: No space left on device", runErr: errors.New("exit status 1"), wantErr: nil},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var gotName string
			var gotArgs []string
			runner := func(name string, args ...string) ([]byte, error) {
				gotName, gotArgs = name, args
				return []byte(tt.output), tt.runErr
			}
			err := InstallWith(runner, "/tmp/staged", "/etc/hosts", 0o644)
			if gotName != "/usr/bin/osascript" {
				t.Errorf("runner name = %q, want /usr/bin/osascript", gotName)
			}
			if len(gotArgs) == 0 || gotArgs[0] != "-e" {
				t.Errorf("runner args = %v, want -e <script>", gotArgs)
			}
			if tt.wantErr == nil {
				if tt.runErr == nil && err != nil {
					t.Fatalf("InstallWith() = %v, want nil", err)
				}
				if tt.runErr != nil && err == nil {
					t.Fatalf("InstallWith() = nil, want wrapped error")
				}
				return
			}
			if !errors.Is(err, tt.wantErr) {
				t.Fatalf("InstallWith() = %v, want errors.Is %v", err, tt.wantErr)
			}
		})
	}
}

// TestInstallWithWrapsRunnerError 确认底层错误被 %w 包装保留（分类之外
// 的故障信息不丢）。
func TestInstallWithWrapsRunnerError(t *testing.T) {
	runner := func(name string, args ...string) ([]byte, error) {
		return []byte("some detail"), fmt.Errorf("spawn osascript: %w", errors.New("no such file"))
	}
	err := InstallWith(runner, "/tmp/staged", "/etc/hosts", 0o644)
	if err == nil || !strings.Contains(err.Error(), "some detail") {
		t.Fatalf("InstallWith() = %v, want detail preserved", err)
	}
}
