package semver

import "testing"

func TestParseAndCompare(t *testing.T) {
	// 业务：版本归一——带/不带 v 前缀、预发布、伪版本的解析与比较。
	cases := []struct {
		name   string
		a, b   string
		want   int
		pseudo bool
		pre    bool
	}{
		{"无前缀版本规范化相等", "1.2.3", "v1.2.3", 0, false, false},
		{"补丁版本更高", "v1.2.4", "v1.2.3", 1, false, false},
		{"正式版高于预发布", "v1.2.0", "v1.2.0-rc.1", 1, false, false},
		{"预发布数字段按数值比较", "v1.0.0-rc.2", "v1.0.0-rc.10", -1, false, true},
		{"伪版本识别", "v0.0.0-20240101000000-abcdef123456", "v0.0.0", -1, true, true},
		{"伪版本日期先后", "v0.0.0-20240101000000-aaaaaaaaaaaa", "v0.0.0-20240102000000-bbbbbbbbbbbb", -1, true, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			va, err := Parse(tc.a)
			if err != nil {
				t.Fatalf("解析 %s 失败: %v", tc.a, err)
			}
			vb, err := Parse(tc.b)
			if err != nil {
				t.Fatalf("解析 %s 失败: %v", tc.b, err)
			}
			if got := Compare(va, vb); got != tc.want {
				t.Errorf("Compare(%s,%s)=%d 期望 %d", tc.a, tc.b, got, tc.want)
			}
			if va.Pseudo != tc.pseudo {
				t.Errorf("Pseudo=%v 期望 %v", va.Pseudo, tc.pseudo)
			}
			if va.IsPrerelease() != tc.pre {
				t.Errorf("IsPrerelease=%v 期望 %v", va.IsPrerelease(), tc.pre)
			}
			// 规范化只断言可往返解析出同一个版本。
			va2, err := Parse(va.String())
			if err != nil || Compare(va2, va) != 0 {
				t.Errorf("规范化往返失败: %q -> %q", tc.a, va.String())
			}
		})
	}
}

func TestParseErrors(t *testing.T) {
	// 业务：畸形版本必须带原因报错，不允许静默吞掉。
	bad := []string{"", "1.2", "v1", "v1.2.x", "vv1.2.3", "v1.2.3-", "v1.2.3.4"}
	for _, s := range bad {
		t.Run("非法版本/"+s, func(t *testing.T) {
			if _, err := Parse(s); err == nil {
				t.Errorf("版本 %q 应解析失败", s)
			}
		})
	}
}

func TestConstraint(t *testing.T) {
	// 业务：版本区间的交集/排除判定与非法区间报错。
	c, err := ParseConstraint(">=1.0.0, <2.0.0")
	if err != nil {
		t.Fatalf("区间解析失败: %v", err)
	}
	checks := map[string]bool{"v1.0.0": true, "v1.9.9": true, "v2.0.0": false, "v0.9.0": false}
	for v, want := range checks {
		ver, _ := Parse(v)
		if got := c.Match(ver); got != want {
			t.Errorf("%s 匹配区间=%v 期望 %v", v, got, want)
		}
	}
	for _, bad := range []string{"", "1.0.0", ">=abc", "~1.0.0"} {
		if _, err := ParseConstraint(bad); err == nil {
			t.Errorf("区间 %q 应解析失败", bad)
		}
	}
}
