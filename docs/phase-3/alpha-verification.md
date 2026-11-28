# Alpha 真机验证清单（#27）

关联 issue：#27。验证对象：GitHub Actions「Alpha packaging」产出的 `mhost-alpha-darwin-arm64` artifact（自签名 .app + DMG，仅 arm64）。

## 安装前

1. 从 PR / Actions 页面下载 artifact 并解压。
2. 自签名应用首次运行需移除隔离属性：
   ```sh
   xattr -dr com.apple.quarantine "/path/to/mHost.app"
   ```

## 启动与数据兼容

- [ ] 应用启动后自动进入 Hosts 页，profile 列表非空（源项目有数据时应列出原 profile；空数据时自动创建「默认」）
- [ ] 用源项目（mHost v0.4.2）的 v1 数据目录启动：横幅提示「Data upgraded from v1 to v2」，数据不丢失
- [ ] 设置页显示正确的数据目录与系统 hosts 路径

## 编辑 → 保存 → 应用 → 回滚闭环

- [ ] 编辑 profile：行内诊断出现（故意写 `999.999.999.999 x.com` 与重复域名验证 error/warning）
- [ ] 保存解析错误内容被拒绝并逐行给出原因
- [ ] 修改 Profile 后点击 Apply…：内容先自动保存，再显示管理块 diff；保存失败时停留在编辑态并显示原因
- [ ] Apply… 预览显示管理块 diff；有冲突时必须选择保留 IP 才能继续
- [ ] 自动保存后取消预览：Profile 保持已保存，系统 Hosts 未修改，并有明确提示
- [ ] Apply 后 `/etc/hosts` 管理块（`# ---- mHost start ----`）内容与预览一致，块外内容未变
- [ ] Apply/Rollback 弹出系统管理员密码框；输入密码后 `/etc/hosts` 管理块写入成功，回执三项齐全且 verification readback 为 true
- [ ] 在密码框点取消：hosts 不变，回执显示"授权已取消"（非可重试错误）
- [ ] 回滚后 `/etc/hosts` 恢复为应用前内容，Header 应用态跟随变化
- [ ] 外部改动管理块后，Header 显示 Drifted

## Profile 管理

- [ ] 新建/复制/启停/删除（受保护 profile 删除被拒绝并说明原因）
- [ ] 删除需二次确认；删除后列表与选中态正确
- [ ] **删除确认弹窗在 Alpha 壳内正确覆盖整个窗口**（壳嵌套了 workbench 的 AlertDialog，需真机确认覆盖层行为）
- [ ] 启停后再编辑保存不报错（草稿修订号同步）
- [ ] 搜索过滤生效
- [ ] 带未保存修改切换 profile 不弹确认，侧栏保留草稿标记，切回后内容仍在
- [ ] 有未保存草稿时 Cmd+Q/托盘退出：显示 Save All、Quit Without Saving、Cancel；Save All 成功后退出，取消后草稿仍在

## 导入导出与页面

- [ ] Export… 导出 `.hosts` 文件到所选目录；Import… 从目录导入并出现在列表
- [ ] 快照页列出已有快照；存在孤儿快照文件时可见 `orphan_snapshot` 提示
- [ ] 系统 Hosts 页只读显示当前 `/etc/hosts` 并定位管理块行号

## 生命周期

- [ ] 关闭窗口后驻留托盘，托盘 Open 可唤回
- [ ] Apply 进行中退出被门禁拦截；若此时仍有其他未保存草稿，操作完成后显示退出选择框，而不是直接退出

## 试用跟踪

验证中发现的问题记录到本文件下方（或新开 issue 关联 #27）；连续试用一周无数据损坏或阻断级问题后关闭 #5。

## 已知限制（验证时不必当 bug 上报）

- 提权走系统管理员密码框（osascript），每次 Apply/Rollback 弹一次（#30 起已接入）
- 快照创建/恢复未开放；DNS/AdBlock 未开放
