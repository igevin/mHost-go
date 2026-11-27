// parse_dump.rs — Rust/Go 差分测试的 Rust 侧 harness。
//
// 用法（在源项目 mHost 仓库内作为 mhost-hosts 的 example 运行）：
//   cargo run --quiet --example parse_dump -- <input-file>
//
// 输出 JSON：与 mHost-go internal/hostsfile 的差分测试期望结构一致
//（见 fixtures/hosts/rust-golden/*.json 与 diff_rust_test.go）。
// 本文件由 mHost-go 仓库 tools/rust-harness/generate.sh 拷入
// <mHost-src>/src-tauri/crates/mhost-hosts/examples/ 后构建运行，
// 不在 Rust 仓库中提交。

use mhost_core::ParseError;
use mhost_hosts::Parser;
use serde::Serialize;

#[derive(Serialize)]
struct OutRule {
    line: usize,
    ip: Option<String>,
    domains: Vec<String>,
    comment: Option<String>,
    comment_only: bool,
    enabled: bool,
}

#[derive(Serialize)]
struct OutError {
    line: usize,
    kind: String,
    token: String,
}

#[derive(Serialize)]
struct OutDuplicate {
    domain: String,
    lines: Vec<usize>,
    kind: String,
}

#[derive(Serialize)]
struct Out {
    rules: Vec<OutRule>,
    errors: Vec<OutError>,
    duplicates: Vec<OutDuplicate>,
}

fn main() {
    let path = std::env::args().nth(1).expect("usage: parse_dump <input-file>");
    let text = std::fs::read_to_string(&path).expect("read input file");

    let result = Parser::parse_with_lines(&text);

    let rules = result
        .rules
        .iter()
        .map(|r| OutRule {
            line: r.line_number.unwrap_or(0),
            ip: r.ip.map(|ip| ip.to_string()),
            domains: r.domains.clone(),
            comment: r.comment.clone(),
            comment_only: r.is_comment_only(),
            enabled: r.enabled,
        })
        .collect();

    let errors = result
        .errors
        .iter()
        .map(|e| {
            let (kind, token) = match &e.error {
                ParseError::InvalidIp(token) => ("invalid_ip", token.clone()),
                ParseError::InvalidDomain(token) => ("invalid_domain", token.clone()),
                ParseError::MalformedLine(line) => ("malformed_line", line.clone()),
            };
            OutError {
                line: e.line_number,
                kind: kind.to_string(),
                token,
            }
        })
        .collect();

    let duplicates = result
        .duplicates
        .iter()
        .map(|d| OutDuplicate {
            domain: d.domain.clone(),
            lines: d.lines.clone(),
            kind: match d.kind {
                mhost_core::DuplicateKind::SameIp => "same_ip".to_string(),
                mhost_core::DuplicateKind::DifferentIp => "different_ip".to_string(),
            },
        })
        .collect();

    let out = Out {
        rules,
        errors,
        duplicates,
    };
    serde_json::to_writer_pretty(std::io::stdout(), &out).expect("serialize output");
    println!();
}
