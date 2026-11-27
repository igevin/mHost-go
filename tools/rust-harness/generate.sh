#!/usr/bin/env bash
# Rust/Go 差分测试 golden 生成脚本。
#
# 用法：
#   MHOST_SRC=/path/to/mHost tools/rust-harness/generate.sh
#
# 把 parse_dump.rs 拷入源项目 mHost 仓库（固定 commit ef1502e）的
# mhost-hosts crate 作为 example 构建，对 fixtures/hosts/corpus/*.txt
# 逐一生成 fixtures/hosts/rust-golden/*.json。
# Go 侧 diff_rust_test.go 用这些 golden 锁定解析行为与源项目一致。
set -euo pipefail

MHOST_SRC="${MHOST_SRC:?set MHOST_SRC to the mHost (Rust) repository checkout}"
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "${SCRIPT_DIR}/../.." && pwd)"
CRATE_EXAMPLES="${MHOST_SRC}/src-tauri/crates/mhost-hosts/examples"

mkdir -p "${CRATE_EXAMPLES}"
cp "${SCRIPT_DIR}/parse_dump.rs" "${CRATE_EXAMPLES}/parse_dump.rs"

CORPUS_DIR="${REPO_ROOT}/fixtures/hosts/corpus"
GOLDEN_DIR="${REPO_ROOT}/fixtures/hosts/rust-golden"
mkdir -p "${GOLDEN_DIR}"

(cd "${MHOST_SRC}/src-tauri" && cargo build --quiet -p mhost-hosts --example parse_dump)

BIN="${MHOST_SRC}/src-tauri/target/debug/examples/parse_dump"
for input in "${CORPUS_DIR}"/*.txt; do
    name="$(basename "${input}" .txt)"
    "${BIN}" "${input}" > "${GOLDEN_DIR}/${name}.json"
    echo "generated ${GOLDEN_DIR}/${name}.json"
done
