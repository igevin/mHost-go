package elevate

import (
	"errors"
	"fmt"
	"strings"
	"testing"
)

// TestInstallScriptQuoting 验证 AppleScript/shell 两级转义：
// 单引号路径按 shell 惯例 '\” 处理，反斜杠与双引号按 AppleScript
// 惯例转义，保证 /etc/hosts 这类固定路径与含特殊字符的临时路径
// 都能安全拼进 do shell script。
func TestInstallScriptQuoting(t *testing.T) {
	tests := []struct {
		name   string
		src    string
		target string
		want   []string // 脚本中必须包含的子串
	}{
		{
			name:   "plain paths",
			src:    "/var/folders/ab/.mhost-hosts-1",
			target: "/etc/hosts",
			want: []string{
				`do shell script `,
				`with administrator privileges`,
				`/bin/cat '/var/folders/ab/.mhost-hosts-1' > '/etc/hosts'`,
				`dscacheutil -flushcache`,
				`killall -HUP mDNSResponder`,
			},
		},
		{
			name:   "spaces are quoted",
			src:    "/tmp/my dir/staged hosts",
			target: "/etc/hosts",
			want:   []string{`'/tmp/my dir/staged hosts'`},
		},
		{
			name:   "single quote escaped for shell",
			src:    "/tmp/it's",
			target: "/etc/hosts",
			want:   []string{`'/tmp/it'\\''s'`},
		},
		{
			name:   "backslash escaped for applescript",
			src:    `/tmp/back\slash`,
			target: "/etc/hosts",
			want:   []string{`back\\slash`},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			script := InstallScript(tt.src, tt.target)
			for _, want := range tt.want {
				if !strings.Contains(script, want) {
					t.Errorf("script missing %q:\n%s", want, script)
				}
			}
		})
	}
}

// TestInstallWithClassification 验证错误分类：用户取消授权（osascript
// 输出 User canceled / -128）必须归类为 ErrCanceled，其余原样返回，
// 服务层据此给出"未改动"的友好提示而不是可重试的写失败。
func TestInstallWithClassification(t *testing.T) {
	tests := []struct {
		name    string
		output  string
		runErr  error
		wantErr error // nil 或哨兵
	}{
		{name: "success", output: "", runErr: nil, wantErr: nil},
		{name: "user canceled en", output: "errAEUserCanceledNumber -128 User canceled.", runErr: errors.New("exit status 1"), wantErr: ErrCanceled},
		{name: "user canceled number only", output: "error number -128", runErr: errors.New("exit status 1"), wantErr: ErrCanceled},
		{name: "generic failure", output: "cat: /etc/hosts: Operation not permitted", runErr: errors.New("exit status 1"), wantErr: nil},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var gotName string
			var gotArgs []string
			runner := func(name string, args ...string) ([]byte, error) {
				gotName, gotArgs = name, args
				return []byte(tt.output), tt.runErr
			}
			err := InstallWith(runner, "/tmp/staged", "/etc/hosts")
			if gotName != "osascript" {
				t.Errorf("runner name = %q, want osascript", gotName)
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
	err := InstallWith(runner, "/tmp/staged", "/etc/hosts")
	if err == nil || !strings.Contains(err.Error(), "some detail") {
		t.Fatalf("InstallWith() = %v, want detail preserved", err)
	}
}
