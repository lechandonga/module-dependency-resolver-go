package semver

import (
	"fmt"
	"strings"
)

// Op 是区间比较运算符。
type Op string

const (
	OpGTE Op = ">="
	OpGT  Op = ">"
	OpLTE Op = "<="
	OpLT  Op = "<"
	OpEQ  Op = "="
	OpNE  Op = "!="
)

// Term 是区间中的一个比较项，如 >=1.2.0。
type Term struct {
	Op  Op
	Ver Version
}

// Constraint 是空格或逗号分隔的一组比较项，语义为交集。
type Constraint struct {
	Terms []Term
	Raw   string
}

// ParseConstraint 解析形如 ">=1.0.0 <2.0.0" 的版本区间。
func ParseConstraint(expr string) (Constraint, error) {
	expr = strings.ReplaceAll(expr, ",", " ")
	var c Constraint
	c.Raw = expr
	for _, tok := range strings.Fields(expr) {
		var opStr, verStr string
		switch {
		case strings.HasPrefix(tok, ">="):
			opStr, verStr = ">=", tok[2:]
		case strings.HasPrefix(tok, "<="):
			opStr, verStr = "<=", tok[2:]
		case strings.HasPrefix(tok, ">"):
			opStr, verStr = ">", tok[1:]
		case strings.HasPrefix(tok, "<"):
			opStr, verStr = "<", tok[1:]
		case strings.HasPrefix(tok, "!="):
			opStr, verStr = "!=", tok[2:]
		case strings.HasPrefix(tok, "=="):
			opStr, verStr = "=", tok[2:]
		case strings.HasPrefix(tok, "="):
			opStr, verStr = "=", tok[1:]
		default:
			return Constraint{}, fmt.Errorf("区间片段 %q 缺少比较运算符（>= > <= < = !=）", tok)
		}
		ver, err := Parse(verStr)
		if err != nil {
			return Constraint{}, fmt.Errorf("区间片段 %q 中的版本非法：%w", tok, err)
		}
		c.Terms = append(c.Terms, Term{Op: Op(opStr), Ver: ver})
	}
	if len(c.Terms) == 0 {
		return Constraint{}, fmt.Errorf("空版本区间")
	}
	return c, nil
}

// Match 报告版本是否满足区间内全部比较项。
func (c Constraint) Match(v Version) bool {
	for _, t := range c.Terms {
		cmp := Compare(v, t.Ver)
		ok := false
		switch t.Op {
		case OpGTE:
			ok = cmp >= 0
		case OpGT:
			ok = cmp > 0
		case OpLTE:
			ok = cmp <= 0
		case OpLT:
			ok = cmp < 0
		case OpEQ:
			ok = cmp == 0
		case OpNE:
			ok = cmp != 0
		}
		if !ok {
			return false
		}
	}
	return true
}
