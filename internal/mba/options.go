package mba

import "strings"

// Options controls optional simplification passes and arithmetic semantics.
type Options struct {
	DeadCode    bool
	ExtendedC   bool
	Signed      bool
	CSE         bool
	ImpureCalls []string
}

func DefaultOptions() Options {
	return Options{ExtendedC: true}
}

func (o Options) impureSet() map[string]bool {
	m := map[string]bool{}
	for _, name := range o.ImpureCalls {
		name = strings.TrimSpace(name)
		if name != "" {
			m[name] = true
		}
	}
	return m
}

func hasImpureCall(e *Expr, impure map[string]bool) bool {
	if e == nil || len(impure) == 0 {
		return false
	}
	if e.Kind == KCall {
		if len(e.Kids) > 0 {
			callee := e.Kids[0]
			if callee.Kind == KVar && impure[callee.Name] {
				return true
			}
		}
	}
	for _, k := range e.Kids {
		if hasImpureCall(k, impure) {
			return true
		}
	}
	return false
}
