// Package root 提供业务覆盖总览测试，方便在 `go test -v` 日志中
// 一眼看出本工具覆盖了哪些业务、各业务有哪些用例。
package root

import "testing"

// TestBusinessCoverageMatrix 不做额外断言，仅以“业务 -> 用例”的形式打印
// 覆盖矩阵；每个用例都由对应包中的具体测试执行并在此登记。
func TestBusinessCoverageMatrix(t *testing.T) {
	business := map[string][]string{
		"畸形清单解析": {
			"缺字段条目带行号报错（manifest.TestMalformedManifestReportsLineAndReason）",
			"非法区间/版本写法按原因报错（manifest/semver）",
			"空分支名、未知指令、未闭合括号块、module 缺失或重复",
			"replace 两侧缺字段或版本非精确",
			"注册数据缺 digest、悬空分支等加载失败（registry.TestLoadRejectsBadData）",
		},
		"版本冲突": {
			"区间无交集报 conflict/range（TestRangeNoIntersectionIsConflict）",
			"不同来源双精确钉版报 conflict/exact（TestDuplicateExactVersionsIsConflict）",
			"精确 pin 与区间不相容（TestExactVersusIncompatibleRangeIsConflict）",
			"唯一版本被 exclude（TestExcludedVersionIsConflict）",
			"模块/分支不存在报 error/missing（TestUnknownBranchAndModuleAreErrors）",
		},
		"传递展开与确定性": {
			"MVS 区间合并选最高共同版本（TestTransitiveMVSAndRanges）",
			"@分支/latest 解析为具体版本（TestBranchAndLatestResolution）",
			"同一输入多次解析结果一致（TestDeterministicAcrossRuns）",
		},
		"循环依赖": {
			"a->b->a 回边报 conflict/cycle 并给出环路径（TestCycleIsConflict）",
		},
		"replace 指令": {
			"指定版本替换满足精确 pin（TestReplaceRemoteVersionAndChain）",
			"替换链成环报 conflict/replace（TestReplaceLoopIsConflict）",
			"同一模块矛盾 replace（TestDuplicateContradictoryReplaceIsConflict）",
			"本地目录替换：目录摘要 + 本地模块传递依赖（TestLocalReplaceHashesDirectoryAndLoadsItsDeps）",
		},
		"锁定与漂移": {
			"输入未变重复 resolve 不改锁内容与 mtime（app.TestResolveThenVerifyIsStable）",
			"清单/注册数据/本地目录输入摘要漂移分类（lock.TestDrift*）",
			"锁版本漂移、摘要不匹配、多余/缺失锁定模块",
			"锁文件损坏/缺字段/重复模块拒绝解析（TestParseRejectsBadLock）",
			"verify 不自动修复被篡改的锁（app.TestVerifyDetectsManuallyEditedLock）",
			"存在冲突时不生成锁文件（app.TestResolveConflictDoesNotWriteLock）",
		},
	}

	// 固定输出顺序，保证日志本身确定。
	for _, b := range []string{"畸形清单解析", "传递展开与确定性", "版本冲突", "循环依赖", "replace 指令", "锁定与漂移"} {
		t.Run(b, func(t *testing.T) {
			for _, c := range business[b] {
				t.Logf("用例：%s", c)
			}
		})
	}
}
