# mdr — 本地模块依赖解析与锁定校验工具

`mdr`（module dependency resolver）用本地清单文件与本地“注册数据”模拟一个模块
仓库，完成依赖声明解析、传递依赖展开、确定性版本选择、冲突标记与锁文件漂移校验。
所有结论都可解释：锁文件里记录每个模块是被哪些来源、以什么版本写法共同选中的。

## 目录结构

```
cmd/mdr              命令行入口
internal/semver      语义化版本、伪版本、版本区间
internal/manifest    清单（modules.txt）解析与归一
internal/registry    本地注册数据（模拟远程仓库）
internal/dirhash     本地 replace 目录的确定性摘要
internal/resolver    传递展开、版本选择、冲突/循环检测
internal/lock        锁文件生成与漂移校验
internal/app         编排 resolve / verify 流程
examples/            可直接运行的示例工作区
```

## 清单语法

清单默认名为 `modules.txt`，支持 `//` 行注释、单/双引号、单行指令与括号块：

```
module example.com/shop

require (
    example.com/auth     v1.4.0            // 精确版本（也含伪版本）
    example.com/billing  ">=1.2.0 <2.0.0"  // 版本区间（引号可省略）
    example.com/flag     @main             // 分支引用，解析时固定到具体版本
    example.com/cache    latest            // 最高稳定版，无稳定版时取最高预发布
    example.com/legacy   v0.0.0-20240101000000-abcdef123456 // Go 风格伪版本
)

exclude example.com/billing v1.3.0

replace example.com/logger v2.0.0 => example.com/logger v1.5.2 // 指定版本替换（可降级）
replace example.com/pay-sdk => ./local/pay-sdk                 // 本地目录替换
replace example.com/old => example.com/new v1.0.0              // 全版本重定向
```

规则：

- 版本允许省略 `v` 前缀，内部统一规范为带 `v` 的形式；预发布遵循 semver
  比较规则（数字段按数值比较，预发布低于正式版）。
- 区间运算符：`>=`、`>`、`<=`、`<`、`=`、`!=`，多个片段用空格或逗号连接，
  语义为交集。
- `exclude` 只接受精确版本；`replace` 左侧版本与右侧远程版本都必须是精确版本，
  右侧本地路径必须以 `./` 或 `../` 开头。
- 本地替换目录中的 `modules.txt` 会作为该本地模块的清单继续参与传递依赖展开；
  没有该文件则视为叶子模块。本地模块版本固定记为 `v0.0.0-local`，锁中保存的是
  目录内容摘要（只与相对路径和文件内容有关，与 mtime/权限无关）。

解析阶段遇到语法错误、非法区间、缺字段条目、未知指令、未闭合括号块、`module`
缺失/重复等问题，都不会被静默跳过：错误按条目逐条返回，包含来源文件、行号、
原始声明文本和具体原因。

## 注册数据

注册数据是一个目录，每个模块一个 JSON 文件（路径与模块路径对应）：

```json
{
  "path": "example.com/billing",
  "versions": [
    {"version": "v1.2.0", "digest": "sha256:...", "requires": [
      {"path": "example.com/logger", "version": ">=1.5.0"}
    ]}
  ],
  "branches": {"main": "v1.2.0"}
}
```

`version`/`digest` 必填，重复版本、悬空分支、缺字段条目都会在加载时报错。

## 版本选择与冲突规则

选择采用 MVS 风格的确定性迭代，直到选择集不动点：

1. 每个模块最终只有一个版本。
2. 纯区间约束：在满足全部约束、且未被 `exclude` 的候选中选择**最高版本**。
3. 唯一精确钉版：必须同时满足所有区间约束；被 `exclude` 或与区间不相容均报冲突。
4. `@分支` 与 `latest` 在参与选择前先固定到注册数据中的具体版本，
   因此分支漂移会改变输入摘要并在校验时暴露。
5. 模块路径、版本列表、迭代顺序全部排序处理；同一份输入多次运行得到字节级一致
   的结论与锁文件。

以下情况**明确标记为冲突/错误，绝不“随便挑一个”**：

| 分类 | 触发条件 |
| --- | --- |
| `conflict/exact` | 不同来源把同一模块钉到不同精确版本 |
| `conflict/range` | 区间交集为空，或精确钉版与区间不相容 |
| `conflict/excluded` | 唯一/选中的版本被 `exclude` 排除 |
| `conflict/cycle` | 依赖图存在环，错误信息给出完整环路径 |
| `conflict/replace` | replace 链成环自相矛盾、同一模块存在互相矛盾的 replace、或固定版本与约束不相容 |
| `error/missing` | 模块/版本/分支/本地目录在本地数据中不存在 |
| `error/malformed` | 清单、传递清单或版本写法畸形（带行号与原因） |

存在任何冲突或错误时不生成/更新锁文件，进程以非零状态退出。

## 锁文件与漂移校验

`resolve` 生成确定性 JSON 锁文件（默认 `modules-lock.json`），内容包括：

- `inputs.manifest` / `inputs.registry` / `inputs.locals`：清单、注册数据目录、
  本地 replace 目录的内容摘要；
- `modules[]`：每个模块选中的版本、内容摘要、replace 信息，以及
  `contributors`——哪些来源（根清单或具体的 `模块 版本`）以什么写法促成了该选择。

语义保证：

- 输入没变时重复执行 `resolve` **不会写入**锁文件（内容与 mtime 都不变）。
- `verify` 重新解析并与现有锁逐项比对，任何漂移都作为**错误**返回，
  **不会自动修复、也不会静默接受**。漂移按原因分类：

| 漂移分类 | 含义 |
| --- | --- |
| `input-manifest-changed` | 清单相对锁定时被改动 |
| `input-registry-changed` | 注册数据目录内容变化 |
| `input-local-changed` | 本地 replace 目录内容变化或已不再使用 |
| `module-version-drift` | 锁版本与重新解析结果不一致 |
| `module-digest-mismatch` | 锁摘要与当前实际内容摘要不一致 |
| `extra-locked-module` | 锁里存在清单（及其依赖图）已不引用的模块 |
| `missing-locked-module` | 重新解析得到的模块在锁里不存在 |
| `root-changed` | 根模块路径不一致 |

## 本地验证

```sh
# 构建
go build ./...

# 全部单元测试（业务分组以中文测试名体现，-v 可看到每个用例归属）
go test ./...
go test -v ./internal/resolver ./internal/lock ./internal/manifest

# 静态检查
go vet ./...

# 示例工作区
go run ./cmd/mdr resolve -manifest examples/modules.txt \
    -registry examples/registry -lock examples/modules-lock.json
go run ./cmd/mdr verify  -manifest examples/modules.txt \
    -registry examples/registry -lock examples/modules-lock.json
```

命令参数：

```
mdr resolve [-manifest modules.txt] [-registry registry] [-lock modules-lock.json]
mdr verify  [-manifest modules.txt] [-registry registry] [-lock modules-lock.json]
```

## 测试覆盖的业务

- 畸形清单：缺字段、非法区间/伪版本、空分支名、未知指令、replace 形态错误、
  未闭合块、`module` 缺失/重复（`internal/manifest`、`internal/app`）。
- 版本冲突：区间无交集、双精确钉版、pin 与区间不相容、exclude 命中、
  缺失模块/分支（`internal/resolver`、`internal/lock`）。
- 循环依赖：a→b→a 环路径被标记（`internal/resolver`）。
- replace：指定版本降级替换、全版本重定向、本地目录替换与传递展开、
  替换链成环、矛盾替换（`internal/resolver`）。
- 锁定漂移：摘要漂移、版本漂移、多余/缺失锁定模块、本地目录变化、
  锁文件损坏、重复解析的幂等与不改 mtime（`internal/lock`、`internal/app`）。
