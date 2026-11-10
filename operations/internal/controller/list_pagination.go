package controller

import (
	"errors"
	"net/http"
	"strconv"
)

type listPage struct {
	Enabled                bool
	Page, PageSize, Offset int
}

// Lists keep their existing array response unless pagination is requested.
func parseListPage(r *http.Request) (listPage, error) {
	query := r.URL.Query()
	p := listPage{Enabled: query.Has("page") || query.Has("pageSize"), Page: 1, PageSize: 100}
	if !p.Enabled {
		return p, nil
	}
	p.PageSize = 10
	for _, field := range []struct {
		name string
		dst  *int
	}{{"page", &p.Page}, {"pageSize", &p.PageSize}} {
		if raw := query.Get(field.name); raw != "" {
			value, err := strconv.Atoi(raw)
			if err != nil || value < 1 {
				return p, errors.New("页码和每页条数须为正整数")
			}
			*field.dst = value
		}
	}
	if p.PageSize > 100 || p.Page-1 > int(^uint(0)>>1)/p.PageSize {
		return p, errors.New("分页范围无效，每页最多100条")
	}
	p.Offset = (p.Page - 1) * p.PageSize
	return p, nil
}

func (p listPage) reply(items any, total int) any {
	if p.Enabled {
		return map[string]any{"items": items, "total": total}
	}
	return items
}
