package resolver

import "sort"

func (r *Resolver) addIssue(i Issue) {
	key := string(i.Type) + "|" + i.Kind + "|" + i.Module + "|" + i.Reason
	if r.issueSet[key] {
		return
	}
	r.issueSet[key] = true
	r.issues = append(r.issues, i)
}

func (r *Resolver) sortedSelectionPaths() []string {
	out := make([]string, 0, len(r.sel))
	for p := range r.sel {
		out = append(out, p)
	}
	sort.Strings(out)
	return out
}

func (r *Resolver) sortedRequirementPaths() []string {
	out := make([]string, 0, len(r.reqs))
	for p := range r.reqs {
		out = append(out, p)
	}
	sort.Strings(out)
	return out
}

func sameSelections(a, b map[string]*Selection) bool {
	if len(a) != len(b) {
		return false
	}
	for p, sa := range a {
		sb, ok := b[p]
		if !ok || sa == nil || sb == nil {
			return false
		}
		if sa.Version != sb.Version || sa.LocalPath != sb.LocalPath || sa.Digest != sb.Digest {
			return false
		}
	}
	return true
}

func uniqueSorted(in []string) []string {
	set := map[string]bool{}
	for _, s := range in {
		set[s] = true
	}
	out := make([]string, 0, len(set))
	for s := range set {
		out = append(out, s)
	}
	sort.Strings(out)
	return out
}

func joinSources(cs []reqConstraint) string {
	seen := map[string]bool{}
	var out []string
	for _, c := range cs {
		s := c.source + " 要求 " + c.raw
		if !seen[s] {
			seen[s] = true
			out = append(out, s)
		}
	}
	sort.Strings(out)
	return joinNonEmpty(out, "；")
}

func dedupContribs(in []Contribution) []Contribution {
	seen := map[string]bool{}
	var out []Contribution
	for _, c := range in {
		key := c.Source + "|" + c.Raw
		if seen[key] {
			continue
		}
		seen[key] = true
		out = append(out, c)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Source != out[j].Source {
			return out[i].Source < out[j].Source
		}
		return out[i].Raw < out[j].Raw
	})
	return out
}

// canonicalCycle 旋转循环路径使其从字典序最小节点开始，用于去重同一循环。
func canonicalCycle(cyc []string) string {
	if len(cyc) <= 1 {
		return joinNonEmpty(cyc, "->")
	}
	body := cyc[:len(cyc)-1] // 去掉重复的首尾
	minIdx := 0
	for i, n := range body {
		if n < body[minIdx] {
			minIdx = i
		}
	}
	rotated := make([]string, 0, len(body)+1)
	for i := 0; i < len(body); i++ {
		rotated = append(rotated, body[(minIdx+i)%len(body)])
	}
	rotated = append(rotated, body[minIdx])
	return joinNonEmpty(rotated, "->")
}

func joinNonEmpty(parts []string, sep string) string {
	if len(parts) == 0 {
		return ""
	}
	out := parts[0]
	for _, p := range parts[1:] {
		out += sep + p
	}
	return out
}

func sortIssues(issues []Issue) {
	sort.SliceStable(issues, func(i, j int) bool {
		if issues[i].Module != issues[j].Module {
			return issues[i].Module < issues[j].Module
		}
		if issues[i].Kind != issues[j].Kind {
			return issues[i].Kind < issues[j].Kind
		}
		return issues[i].Reason < issues[j].Reason
	})
}
