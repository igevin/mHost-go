# Phase 0 fixtures

所有数据均为虚构或程序生成，不来自真实用户目录。

- `storage/v1-hosts`：v1 Manifest 和缺失 `mode` 的 Hosts Profile。
- `storage/v2-full`：v2 Manifest、两种 Profile、设置合成视图、快照及广告屏蔽配置；包含重复、冲突、多域名和注释规则。
- `storage/v2-legacy-empty`：旧 `original_dns: []`，应解释为 DHCP/Empty。
- `storage/v2-legacy-placeholder`：旧 `original_dns: ["Empty"]`，应解释为 DHCP/Empty。
- `storage/v2-legacy-filtered`：旧数组混有 loopback，应过滤后保留公网 DNS。
- `storage/v2-tagged-dhcp`：新 `{"kind":"dhcp_empty"}` 表示；tagged manual 仅含 loopback 时同样降级为 DHCP/Empty（由单元测试覆盖）。
- `storage/mixed-invalid`：一个有效 Profile 与损坏 JSON、缺字段、错类型文件并存，用于验证逐文件错误。
- `hosts/sample.hosts`：编辑器与诊断的小型合成文本。

`v2-full/settings.json` 是源项目 localStorage 偏好的合成视图，不表示源项目已经存在该磁盘文件。快照索引、pending 目录和广告缓存的完整恢复行为不属于只读探针范围。

生成 100,000 行 Hosts 冒烟样本：

```sh
go run ./cmd/hosts-fixture -lines 100000 -output /tmp/mhost-phase0.hosts
```

生成器使用固定序列，同一行数产生相同内容。大文件不提交仓库。
