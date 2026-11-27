// Package alphaboot 完成 Hosts-only Alpha 的首次启动准备（#25）：
// 定位数据目录 → v1 数据原地迁移（存储服务）→ 缺省 profile 兜底 →
// 构造真实 HostsService。返回迁移回执与扫描 issue 供 UI 展示。
package alphaboot

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"time"

	app "github.com/igevin/mHost-go/internal/app"
	"github.com/igevin/mHost-go/internal/app/hostssvc"
	"github.com/igevin/mHost-go/internal/storage"
)

// DefaultHostsPath 是各平台系统 hosts 路径（Alpha 支持 darwin/linux）。
const DefaultHostsPath = "/etc/hosts"

// Result 是启动准备的结果。
type Result struct {
	DataDir   string             // 工作区根（v2 存储所在处）
	HostsPath string             // 系统 hosts 路径
	Service   *hostssvc.Service  // 真实 HostsService
	Migration *app.WriteReceipt  // 非 nil 表示本次启动完成了 v1→v2 迁移
	Issues    []app.StorageIssue // 扫描/迁移产生的 informational issue
}

// Options 覆盖启动默认值（测试注入临时目录与 hosts 文件）。
type Options struct {
	DataDir       string // 默认 os.UserConfigDir()/mHost
	HostsPath     string // 默认 /etc/hosts；MHOST_HOSTS_PATH 环境变量优先
	SkipMigration bool
}

// DefaultProfileContent 是空存储时创建的兜底 profile 内容。
const DefaultProfileContent = `# mHost 默认 profile
# 每行一条规则：IP 域名 [注释]；# 开头为注释行
127.0.0.1 localhost
`

const defaultProfileName = "默认"

// Prepare 执行启动准备。任何一步失败返回 error（UI 展示后退出）。
func Prepare(ctx context.Context, opts Options) (*Result, error) {
	dataDir := opts.DataDir
	if dataDir == "" {
		base, err := os.UserConfigDir()
		if err != nil {
			return nil, &app.UIError{Kind: app.ErrorInvalidPath, Message: "Cannot locate the user config directory", Cause: err}
		}
		dataDir = filepath.Join(base, "mHost")
	}
	hostsPath := opts.HostsPath
	if hostsPath == "" {
		hostsPath = os.Getenv("MHOST_HOSTS_PATH")
	}
	if hostsPath == "" {
		hostsPath = DefaultHostsPath
	}

	if err := os.MkdirAll(dataDir, 0o700); err != nil {
		return nil, &app.UIError{Kind: app.ErrorPermission, Message: "Cannot create the data directory", Path: dataDir, Cause: err}
	}

	storageService := storage.NewService()
	var migration *app.WriteReceipt
	var issues []app.StorageIssue

	if !opts.SkipMigration {
		// 无 manifest = 全新安装，跳过存储检查与迁移
		if _, err := os.Stat(filepath.Join(dataDir, storage.FileManifest)); err == nil {
			workspace, err := storageService.InspectWritable(ctx, dataDir)
			if err != nil {
				return nil, err
			}
			issues = append(issues, workspace.Summary.Issues...)
			if workspace.StorageVersion < 2 {
				plan, err := storageService.PreviewMigration(ctx, workspace.ID)
				if err != nil {
					return nil, err
				}
				issues = append(issues, plan.Blockers...)
				if len(plan.Blockers) == 0 {
					receipt, err := storageService.ApplyMigration(ctx, workspace.ID, plan.ID)
					if receipt.Results != nil {
						migration = &receipt
					}
					for _, result := range receipt.Results {
						if result.Issue != nil {
							issues = append(issues, *result.Issue)
						}
					}
					if err != nil {
						// 迁移失败：回执已带出问题明细，整体失败由 UI 决定重试/退出
						return &Result{DataDir: dataDir, HostsPath: hostsPath, Migration: migration, Issues: issues}, err
					}
				}
			}
		} else if os.IsNotExist(err) {
			// 全新安装：写出最小 v2 manifest，让存储扫描/快照页可用
			manifest := storage.Manifest{
				Version:     2,
				AppVersion:  "0.3.0-alpha",
				UpdatedAt:   time.Now().UTC(),
				DNSEnabled:  boolPtr(false),
				OriginalDNS: &storage.OriginalDNS{Kind: storage.OriginalDNSDHCPEmpty},
			}
			data, err := json.MarshalIndent(manifest, "", "  ")
			if err != nil {
				return nil, err
			}
			volume := storage.NewOSVolume()
			if err := volume.WriteAtomic(filepath.Join(dataDir, storage.FileManifest), data, storage.FilePerm); err != nil {
				return nil, &app.UIError{Kind: app.ErrorWrite, Message: "Cannot initialize the data directory", Path: dataDir, Cause: err}
			}
		} else {
			return nil, &app.UIError{Kind: app.ErrorPermission, Message: "Cannot read the data directory", Path: dataDir, Cause: err}
		}
	}

	volume := storage.NewOSVolume()
	hostsService := hostssvc.New(dataDir, hostsPath, filepath.Join(dataDir, "backups"), volume)

	profiles, err := hostsService.ListProfiles(ctx)
	if err != nil {
		return nil, err
	}
	if len(profiles) == 0 {
		if _, err := hostsService.CreateProfile(ctx, defaultProfileName, DefaultProfileContent); err != nil {
			return nil, err
		}
	}

	return &Result{
		DataDir:   dataDir,
		HostsPath: hostsPath,
		Service:   hostsService,
		Migration: migration,
		Issues:    issues,
	}, nil
}

func boolPtr(v bool) *bool { return &v }
