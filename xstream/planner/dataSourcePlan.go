package planner

import (
	"fmt"
	"github.com/emqx/kuiper/common"
	"github.com/emqx/kuiper/xsql"
	"sort"
	"strings"
)

type DataSourcePlan struct {
	baseLogicalPlan
	name string
	// calculated properties
	// initialized with stream definition, pruned with rule
	streamFields []interface{}
	metaFields   []string
	// passon properties
	streamStmt      *xsql.StreamStmt
	alias           xsql.Fields
	allMeta         bool
	isBinary        bool
	iet             bool
	timestampFormat string
	timestampField  string
	// intermediate status
	isWildCard bool
	fields     map[string]interface{}
	metaMap    map[string]bool
}

func (p DataSourcePlan) Init() *DataSourcePlan {
	p.baseLogicalPlan.self = &p
	return &p
}

// Presume no children for data source
func (p *DataSourcePlan) PushDownPredicate(condition xsql.Expr) (xsql.Expr, LogicalPlan) {
	owned, other := p.extract(condition)
	if owned != nil {
		// Add a filter plan for children
		f := FilterPlan{
			condition: owned,
		}.Init()
		f.SetChildren([]LogicalPlan{p})
		return other, f
	}
	return other, p
}

func (p *DataSourcePlan) extract(expr xsql.Expr) (xsql.Expr, xsql.Expr) {
	s := getRefSources(expr)
	switch len(s) {
	case 0:
		return expr, nil
	case 1:
		if s[0] == p.name || s[0] == "" {
			return expr, nil
		} else {
			return nil, expr
		}
	default:
		if be, ok := expr.(*xsql.BinaryExpr); ok && be.OP == xsql.AND {
			ul, pl := p.extract(be.LHS)
			ur, pr := p.extract(be.RHS)
			owned := combine(ul, ur)
			other := combine(pl, pr)
			return owned, other
		}
		return nil, expr
	}
}

func (p *DataSourcePlan) PruneColumns(fields []xsql.Expr) error {
	//init values
	p.getProps()
	p.fields = make(map[string]interface{})
	if !p.allMeta {
		p.metaMap = make(map[string]bool)
	}
	if p.timestampField != "" {
		p.fields[p.timestampField] = p.timestampField
	}
	for _, field := range fields {
		switch f := field.(type) {
		case *xsql.Wildcard:
			p.isWildCard = true
		case *xsql.FieldRef:
			if !p.isWildCard && (f.StreamName == "" || string(f.StreamName) == p.name) {
				if _, ok := p.fields[f.Name]; !ok {
					sf := p.getField(f.Name)
					if sf != nil {
						p.fields[f.Name] = sf
					}
				}
			}
		case *xsql.MetaRef:
			if p.allMeta {
				break
			}
			if f.StreamName == "" || string(f.StreamName) == p.name {
				if f.Name == "*" {
					p.allMeta = true
					p.metaMap = nil
				} else if !p.allMeta {
					p.metaMap[f.Name] = true
				}
			}
		case *xsql.SortField:
			if !p.isWildCard {
				sf := p.getField(f.Name)
				if sf != nil {
					p.fields[f.Name] = sf
				}
			}
		default:
			return fmt.Errorf("unsupported field %v", field)
		}
	}
	p.getAllFields()
	return nil
}

func (p *DataSourcePlan) getField(name string) interface{} {
	if p.streamStmt.StreamFields != nil {
		for _, f := range p.streamStmt.StreamFields { // The input can only be StreamFields
			if f.Name == name {
				return &f
			}
		}
	} else {
		return name
	}
	return nil
}

func (p *DataSourcePlan) getAllFields() {
	// convert fields
	p.streamFields = make([]interface{}, 0)
	if p.isWildCard {
		if p.streamStmt.StreamFields != nil {
			for k, _ := range p.streamStmt.StreamFields { // The input can only be StreamFields
				p.streamFields = append(p.streamFields, &p.streamStmt.StreamFields[k])
			}
		} else {
			p.streamFields = nil
		}
	} else {
		sfs := make([]interface{}, 0, len(p.fields))
		if common.IsTesting {
			var keys []string
			for k, _ := range p.fields {
				keys = append(keys, k)
			}
			sort.Strings(keys)
			for _, k := range keys {
				sfs = append(sfs, p.fields[k])
			}
		} else {
			for _, v := range p.fields {
				sfs = append(sfs, v)
			}
		}
		p.streamFields = sfs
	}
	p.metaFields = make([]string, 0, len(p.metaMap))
	for k, _ := range p.metaMap {
		p.metaFields = append(p.metaFields, k)
	}
	p.fields = nil
	p.metaMap = nil
}

func (p *DataSourcePlan) getProps() error {
	if p.iet {
		if tf, ok := p.streamStmt.Options["TIMESTAMP"]; ok {
			p.timestampField = tf
		} else {
			return fmt.Errorf("preprocessor is set to be event time but stream option TIMESTAMP not found")
		}
		if ts, ok := p.streamStmt.Options["TIMESTAMP_FORMAT"]; ok {
			p.timestampFormat = ts
		}
	}
	if f, ok := p.streamStmt.Options["FORMAT"]; ok {
		if strings.ToLower(f) == common.FORMAT_BINARY {
			p.isBinary = true
		}
	}
	return nil
}
