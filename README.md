# module-dependency-resolver-go

本地的模块依赖解析与锁定校验工具。用本地 JSON 清单和本地 registry 文件模拟依赖数据，
把写法各异、甚至互相矛盾的依赖声明解析成一致、可解释、可复现的结论。

## 组成

| 包 | 职责 |
|---|---|
| `internal/semver` | 版本 / 伪版本解析与比较，区间约束解析与匹配 |
| `manifest` | 依赖清单解析与归一，按原因分类的条目级报错 |
| `registry` | 本地模块数据源（版本、内容摘要、分支头、各版本自身的依赖） |
| `resolver` | 传递依赖展开、确定性版本选择、冲突检测 |
| `lock` | 锁文件生成与漂移校验 |
| `cmd/depres` | 命令行入口（resolve / verify / graph） |

## 清单语法

清单是 JSON 文件（示例见 `examples/dep.json`）：

```json
{
  "module": "corp/app",
  "requires": [
    {"path": "corp/http", "version": "[1.0.0,2.0.0)"},
    {"path": "corp/log",  "version": ">=1.0.0 <2.0.0"},
    {"path": "corp/pin",  "version": "v1.2.3"},
    {"path": "corp/util", "version": "v0.0.0-20240102030405-abcdef123456"},
    {"path": "corp/exp",  "ref": "branch:main"}
  ],
  "excludes": [{"path": "corp/log", "version": "v1.3.0"}],
  "replaces": [{"path": "corp/old", "with": "corp/new", "version": "v2.0.0"}]
}
```

`version` 支持的写法：

- **精确版本**：`v1.2.3`
- **伪版本**：`v0.0.0-yyyymmddhhmmss-abcdef123456`（14 位时间戳 + 12 位十六进制提交号）
- **比较器区间**：`>=1.0.0 <2.0.0`（空格或逗号分隔，支持 `> >= < <= ==`）
- **区间记号**：`[1.0.0,2.0.0)`（`[`/`]` 闭、`(`/`)` 开，边界可省略 `v` 前缀）

`ref` 目前支持 `branch:<名字>`，解析为 registry 中该分支当前指向的版本。
`version` 与 `ref` 互斥。

### 解析错误分类

坏条目不会被静默跳过，所有错误一次性聚合报出，并定位到具体声明（如 `require[1](corp/log)`）：

| kind | 含义 |
|---|---|
| `syntax_error` | 文件不是合法 JSON / 含未知字段 |
| `missing_field` | 缺 path / version / module 等必填字段 |
| `invalid_version` | 版本串非法（含伪版本格式错误） |
| `invalid_range` | 区间写法非法或区间为空（下界 ≥ 上界） |
| `unknown_field` | version 与 ref 同时出现、或不支持的 ref 类型 |
| `duplicate_directive` | 同一模块被 require/replace 重复声明 |

## 版本选择与冲突规则

**选择规则（确定性）**：对每个模块，收集所有引入来源贡献的约束，取交集；
剔除 `exclude` 的版本；`replace` 先把路径重定向（可钉死版本）再参与选择；
最终在交集内选**已发布的最高版本**。同一输入多次运行结果逐字节一致。

**冲突分类**（出现冲突时不挑选、不猜测，该模块不进入结果）：

| kind | 触发条件 |
|---|---|
| `no_version_intersection` | 单一来源的约束交集为空 |
| `duplicate_introduction` | 同一模块被多个来源引入且约束无公共版本（报错附来源列表） |
| `dependency_cycle` | 选中图中存在环（展开仍可终止，环被单独标记） |
| `replace_chain_conflict` | replace 链成环，或链上钉死的版本互相矛盾 |
| `missing_module` | 模块 / 分支 / 版本在 registry 中不存在 |

## 锁定与漂移校验

`depres resolve` 生成锁文件（示例见 `examples/dep.lock`）：

```json
{
  "manifest_hash": "sha256:...",
  "entries": [{"path": "corp/http", "version": "v1.2.0", "digest": "sha256:..."}]
}
```

- 每个条目记录选中版本与内容摘要（`sha256(path\0version\0content)`）。
- 输入不变时重复 `resolve` 生成的锁文件逐字节一致（幂等）。
- `depres verify` 重新解析并逐项比对，**只报错、不自动修复**，漂移分类：

| kind | 含义 |
|---|---|
| `manifest_changed` | 清单内容哈希与锁中记录不符（清单被手改） |
| `stale_entry` | 锁中有清单已不再引用的模块 |
| `missing_entry` | 解析选中的模块在锁中缺失 |
| `version_mismatch` | 锁定版本与重新解析结果不一致 |
| `digest_mismatch` | 锁定摘要与 registry 内容摘要不符 |
| `resolution_conflict` | 重新解析本身出现冲突 |

## 本地验证

```sh
# 运行全部单测（-v 日志可看到各业务覆盖与用例明细）
go test ./... -v

# 端到端示例
go run ./cmd/depres resolve -manifest examples/dep.json -registry examples/registry.json -lock examples/dep.lock
go run ./cmd/depres verify  -manifest examples/dep.json -registry examples/registry.json -lock examples/dep.lock
go run ./cmd/depres graph   -manifest examples/dep.json -registry examples/registry.json
```

测试按业务组织：

- `manifest`：清单归一化（精确/区间/伪版本/分支/replace/exclude）、畸形清单分类报错、错误聚合
- `resolver`：传递展开与版本选择、确定性、区间无交集、循环依赖、replace 链冲突、重复引入、exclude、分支引用
- `lock`：锁生成与校验、幂等、清单篡改、残留条目、摘要漂移、版本漂移、缺失条目
