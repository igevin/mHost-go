# Phase 0 macOS 真机验证手册

> **历史归档**：本文仅记录 Phase 0 原型及其历史 artifact 的验证步骤，不适用于当前 `mHost Phase 1.app`、`mhost-phase-1-darwin-arm64` 或 Storage Workbench。当前 UI 验证计划见 [Phase 1 Storage Workbench 验证计划](../phase-1/storage-ui-validation.md)。

## 适用范围

仅在一台用于测试的 M 系列 Mac 上执行。不要在工作生产环境、真实用户数据目录或需要持续联网的主力环境测试 DNS。GitHub Actions 只验证构建、签名、架构和 proxy 非提权自检，不执行下列系统 DNS 操作。

## 1. 下载与静态检查

1. 打开目标 PR 的 `Phase 0 validation` workflow run。
2. 下载 `mhost-phase-0-darwin-arm64` artifact 并解压到独立目录。
3. 在包含 DMG 的目录运行：

   ```sh
   shasum -a 256 -c SHA256SUMS
   ```

4. 打开 `build-metadata.txt`，确认 commit、Go/MyGo 版本和 DMG 文件名属于待验证构建。
5. 挂载 DMG，将 `mHost Phase 0.app` 复制到独立测试目录。记录该路径为 `APP`：

   ```sh
   APP="/absolute/path/to/mHost Phase 0.app"
   MAIN="$APP/Contents/MacOS/$(plutil -extract CFBundleExecutable raw -o - "$APP/Contents/Info.plist")"
   PROXY="$APP/Contents/Resources/bin/mhost-dns-proxy"
   RUNTIME="$HOME/Library/Application Support/mHost Phase 0/phase0-runtime"
   ```

6. 验证架构、签名和包内 proxy：

   ```sh
   lipo -archs "$MAIN"
   lipo -archs "$PROXY"
   codesign --verify --strict "$PROXY"
   codesign --verify --strict "$APP"
   "$PROXY" -check
   ```

   两次 `lipo` 只能输出 `arm64`；proxy 应输出 `mhost-dns-proxy phase0 self-check ok`。

7. 首次打开时在“隐私与安全性”中显式允许，或只对这个测试包执行：

   ```sh
   xattr -dr com.apple.quarantine "$APP"
   open "$APP"
   ```

不运行 `spctl`，Gatekeeper 的额外提示是 ad-hoc 测试包的预期行为。

## 2. UI 与编辑器

- [ ] 主窗口显示静态 Sidebar、Hosts 编辑器、DNS Lab 和设置占位入口。
- [ ] 编辑文本后 Cmd-Z 撤销、Shift-Cmd-Z 重做。
- [ ] Cmd-F 搜索，Enter/Shift-Enter 移动匹配；Cmd-Option-F 替换单个或全部匹配。
- [ ] Ctrl-G 跳转到指定行，行号、选区和状态栏同步。
- [ ] 点击 `Load 100k`，等待状态栏显示合成文本已加载；搜索 `host-099999.phase0.test`，跳转末行并连续滚动，不出现卡死或阻断性卡顿。
- [ ] `About Phase 0` 打开普通弹窗，Close 关闭。
- [ ] 修改文本、移动选区并滚动后关闭窗口；窗口隐藏而不是退出。通过托盘 `Open mHost` 重新打开，文本、撤销历史、选区和滚动位置保留。
- [ ] 仅隐藏窗口不清理正在运行的 DNS 测试。

记录实际结果，不用耗时或内存数字做性能对照。

## 3. DNS 正常路径

先记录活动网络服务的原始 DNS。原型默认通过默认路由识别服务，并只允许白名单中的服务名；以下示例以 Wi-Fi 为例：

```sh
networksetup -getdnsservers Wi-Fi
```

在 DNS Lab 点击 `Start DNS Test` 并完成管理员授权。只有这个显式操作可以修改 DNS。

```sh
networksetup -getdnsservers Wi-Fi
dig @127.0.0.1 phase0.test A +short
```

预期系统 DNS 包含 `127.0.0.1`，查询返回 `192.0.2.53`。点击 `Stop DNS Test` 后再次读取系统 DNS，确认恢复原值；DHCP/Empty 场景应恢复为没有手动 DNS，而不是写死当时的 DHCP 地址。状态栏必须显示恢复已验证。

## 4. 退出与异常矩阵

每轮先确认原始 DNS，再启动 DNS 测试。每轮结束都读取系统 DNS，并检查 `recovery_pending`：只有恢复并验证成功后 marker 才允许消失。

| 场景 | 操作 | 预期结果 | 实际结果 |
| --- | --- | --- | --- |
| 正常 Stop | 点击 Stop DNS Test | proxy 停止，原 DNS 读回一致，marker 删除 | |
| Tray Quit | DNS 运行中选择托盘 Quit | 应用退出，原 DNS 恢复，marker 删除 | |
| 窗口可见 Cmd-Q | 主窗口可见时按 Cmd-Q | 同上 | |
| 仅托盘 Cmd-Q | 隐藏窗口后按 Cmd-Q | 同上 | |
| SIGTERM | 从终端启动应用并取得 PID；DNS 运行后 `kill -TERM "$APP_PID"` | 应用退出，原 DNS 恢复 | |
| proxy 启动失败 | Start 时取消管理员授权，或制造受控的 53 端口占用 | Start 返回错误，proxy 不遗留，原 DNS 恢复；失败时 marker 保留 | |
| proxy 运行中退出 | `sudo kill -TERM "$(cat "$RUNTIME/proxy.pid")"` | 应用监测到退出并恢复原 DNS | |
| 应用 SIGKILL | DNS 运行后 `kill -9 "$APP_PID"`，随后重新打开应用 | 强杀后 marker 保留；下次启动停止遗留 proxy、恢复并验证原 DNS，再删除 marker | |

从终端启动并取得 PID：

```sh
"$MAIN" &
APP_PID=$!
```

检查状态文件：

```sh
ls -l "$RUNTIME"
```

如恢复失败，marker 必须保留。重新启动应用应重试。不要手工删除 `recovery_pending` 来让测试“通过”。若需要紧急人工恢复，使用最初记录的 DNS 值；DHCP/Empty 场景执行 `sudo networksetup -setdnsservers Wi-Fi Empty`，然后把实际情况写入报告。

## 5. 结果回填

将 Mac 型号、macOS 版本、构建 commit、每一项实际结果、截图/日志位置和失败 workaround 写入 `report.md`。任何未执行项保持未勾选。若核心退出行为必须依赖长期私有 MyGo fork、DNS 无法可靠恢复或包内 proxy 权限链路不可用，Phase 0 结论必须是调整或停止，而不是继续。
