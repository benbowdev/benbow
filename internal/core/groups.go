// SPDX-License-Identifier: GPL-3.0-only
// Copyright (C) 2026 Andrea Casali

package core

import (
	"context"
	"slices"
	"strconv"
	"strings"
	"time"
	"unicode"

	"github.com/getcovo/covo/internal/errcode"
	"github.com/getcovo/covo/internal/store"
)

// Groups organize the site list into nested folders. They only matter to
// the list: how a site is served never depends on its group.

// GroupInfo is a group with its full path ("Clients/Acme").
type GroupInfo struct {
	store.Group
	Path string `json:"path"`
}

// maxGroupDepth keeps paths readable and cycles impossible to miss.
const maxGroupDepth = 16

// ListGroups returns every group in list order, each followed by its subgroups.
func (c *Core) ListGroups(ctx context.Context) ([]GroupInfo, error) {
	groups, err := c.Store.ListGroups(ctx)
	if err != nil {
		return nil, err
	}
	return groupInfos(groups), nil
}

func groupInfos(groups []store.Group) []GroupInfo {
	byID := map[int64]store.Group{}
	for _, g := range groups {
		byID[g.ID] = g
	}
	// Depth first, each level in list order (groups come from the store
	// ordered by position, then name).
	out := make([]GroupInfo, 0, len(groups))
	seen := map[int64]bool{}
	var walk func(parent int64, depth int)
	walk = func(parent int64, depth int) {
		if depth > maxGroupDepth {
			return
		}
		for _, g := range groups {
			if g.ParentID == parent && !seen[g.ID] {
				seen[g.ID] = true
				out = append(out, GroupInfo{Group: g, Path: groupPath(byID, g)})
				walk(g.ID, depth+1)
			}
		}
	}
	walk(0, 0)
	// Groups whose parent is missing (never expected) still show up.
	for _, g := range groups {
		if !seen[g.ID] {
			out = append(out, GroupInfo{Group: g, Path: groupPath(byID, g)})
		}
	}
	return out
}

// ListOrder is the order of one level of the site list: groups
// ("group:<id>") and sites ("site:<name>"), mixed, inside Group (0: top
// level). Items listed from elsewhere move into Group; items of Group not
// listed follow, in their current order.
type ListOrder struct {
	Group int64    `json:"group"`
	Items []string `json:"items"`
}

// levelOrder is the current order of a level: by position (shared by
// groups and sites); in a level never ordered, groups then sites by name.
func levelOrder(groups []store.Group, sites []store.Site, parent int64) []string {
	type entry struct {
		token, name string
		pos         int
		group       bool
	}
	var es []entry
	for _, g := range groups {
		if g.ParentID == parent {
			es = append(es, entry{"group:" + strconv.FormatInt(g.ID, 10), g.Name, g.Position, true})
		}
	}
	for _, st := range sites {
		if st.GroupID == parent {
			es = append(es, entry{"site:" + st.Name, st.Name, st.Position, false})
		}
	}
	slices.SortStableFunc(es, func(a, b entry) int {
		if a.pos != b.pos {
			return a.pos - b.pos
		}
		if a.group != b.group {
			if a.group {
				return -1
			}
			return 1
		}
		return strings.Compare(strings.ToLower(a.name), strings.ToLower(b.name))
	})
	out := make([]string, len(es))
	for i, e := range es {
		out[i] = e.token
	}
	return out
}

// SetListOrder orders (and moves) groups and sites inside one level.
func (c *Core) SetListOrder(ctx context.Context, o ListOrder) (ListOrder, error) {
	var res ListOrder
	err := c.locked(ctx, func() error {
		groups, err := c.Store.ListGroups(ctx)
		if err != nil {
			return err
		}
		if !groupExists(groups, o.Group) {
			return errcode.Newf(errcode.NotFound, "group %d not found", o.Group)
		}
		sites, err := c.Store.ListSites(ctx)
		if err != nil {
			return err
		}
		byID := map[int64]store.Group{}
		for _, g := range groups {
			byID[g.ID] = g
		}
		known := map[string]bool{}
		for _, st := range sites {
			known[st.Name] = true
		}
		var items []store.OrderItem
		var tokens []string
		seen := map[string]bool{}
		add := func(tok string) error {
			if seen[tok] {
				return nil
			}
			seen[tok] = true
			switch {
			case strings.HasPrefix(tok, "site:"):
				name := strings.TrimPrefix(tok, "site:")
				if !known[name] {
					return errcode.Newf(errcode.NotFound, "site %q not found", name)
				}
				items = append(items, store.OrderItem{Site: name})
			case strings.HasPrefix(tok, "group:"):
				id, err := strconv.ParseInt(strings.TrimPrefix(tok, "group:"), 10, 64)
				g, ok := byID[id]
				if err != nil || !ok {
					return errcode.Newf(errcode.NotFound, "group %s not found", strings.TrimPrefix(tok, "group:"))
				}
				if g.ParentID != o.Group {
					if insideGroup(groups, o.Group, id) {
						return errcode.Errorf(errcode.InvalidInput, "a group cannot go inside itself")
					}
					if depth(groups, o.Group)+subtreeHeight(groups, id) > maxGroupDepth {
						return errcode.Newf(errcode.InvalidInput, "groups nest at most %d levels", maxGroupDepth)
					}
					if err := checkGroupName(groups, o.Group, g.Name, id); err != nil {
						return err
					}
				}
				items = append(items, store.OrderItem{Group: id})
			default:
				return errcode.Newf(errcode.InvalidInput, "%q: name items site:<name> or group:<id>", tok)
			}
			tokens = append(tokens, tok)
			return nil
		}
		for _, tok := range o.Items {
			if err := add(tok); err != nil {
				return err
			}
		}
		for _, tok := range levelOrder(groups, sites, o.Group) { // the rest of the level
			if err := add(tok); err != nil {
				return err
			}
		}
		if err := c.Store.SetOrder(ctx, o.Group, items); err != nil {
			return err
		}
		res = ListOrder{Group: o.Group, Items: tokens}
		return nil
	})
	return res, err
}

func groupPath(byID map[int64]store.Group, g store.Group) string {
	parts := []string{g.Name}
	for p, depth := g.ParentID, 0; p != 0 && depth < maxGroupDepth; depth++ {
		parent, ok := byID[p]
		if !ok {
			break
		}
		parts = append(parts, parent.Name)
		p = parent.ParentID
	}
	slices.Reverse(parts)
	return strings.Join(parts, "/")
}

// cleanGroupName trims a group name and checks it.
func cleanGroupName(name string) (string, error) {
	name = strings.TrimSpace(name)
	if name == "" || len(name) > 64 {
		return "", errcode.Errorf(errcode.InvalidInput, "a group name must be 1 to 64 characters")
	}
	if strings.ContainsRune(name, '/') || strings.ContainsFunc(name, unicode.IsControl) {
		return "", errcode.Newf(errcode.InvalidInput, "group name %q must not contain / or control characters", name)
	}
	return name, nil
}

// checkGroupName fails if parent already has a group called name (ignoring
// case), other than self.
func checkGroupName(groups []store.Group, parent int64, name string, self int64) error {
	for _, g := range groups {
		if g.ParentID == parent && g.ID != self && strings.EqualFold(g.Name, name) {
			return errcode.Newf(errcode.Conflict, "there is already a group called %q there", g.Name)
		}
	}
	return nil
}

// depth is the number of groups from the top level down to id (1 for a
// top-level group, 0 for the top level itself).
func depth(groups []store.Group, id int64) int {
	byID := map[int64]int64{}
	for _, g := range groups {
		byID[g.ID] = g.ParentID
	}
	d := 0
	for ; id != 0 && d <= maxGroupDepth; d++ {
		id = byID[id]
	}
	return d
}

func groupExists(groups []store.Group, id int64) bool {
	return id == 0 || slices.ContainsFunc(groups, func(g store.Group) bool { return g.ID == id })
}

// AddGroup creates a group called name inside parent (0: top level).
func (c *Core) AddGroup(ctx context.Context, name string, parent int64) (GroupInfo, error) {
	name, err := cleanGroupName(name)
	if err != nil {
		return GroupInfo{}, err
	}
	var res GroupInfo
	err = c.locked(ctx, func() error {
		groups, err := c.Store.ListGroups(ctx)
		if err != nil {
			return err
		}
		if !groupExists(groups, parent) {
			return errcode.Newf(errcode.NotFound, "group %d not found", parent)
		}
		if depth(groups, parent) >= maxGroupDepth {
			return errcode.Newf(errcode.InvalidInput, "groups nest at most %d levels", maxGroupDepth)
		}
		if err := checkGroupName(groups, parent, name, 0); err != nil {
			return err
		}
		g := store.Group{Name: name, ParentID: parent, CreatedAt: time.Now()}
		if g.ID, err = c.Store.AddGroup(ctx, g); err != nil {
			return err
		}
		res, err = c.groupInfo(ctx, g.ID)
		return err
	})
	return res, err
}

func (c *Core) groupInfo(ctx context.Context, id int64) (GroupInfo, error) {
	all, err := c.ListGroups(ctx)
	if err != nil {
		return GroupInfo{}, err
	}
	for _, g := range all {
		if g.ID == id {
			return g, nil
		}
	}
	return GroupInfo{}, errcode.Newf(errcode.NotFound, "group %d not found", id)
}

// UpdateGroupRequest renames a group or moves it into another one.
type UpdateGroupRequest struct {
	ID   int64
	Name string // "" keeps the name
	// Parent, when set, moves the group (0: top level).
	Parent *int64
}

// UpdateGroup renames or moves a group. A group cannot move into itself or
// one of its own subgroups.
func (c *Core) UpdateGroup(ctx context.Context, req UpdateGroupRequest) (GroupInfo, error) {
	var res GroupInfo
	err := c.locked(ctx, func() error {
		g, err := c.Store.Group(ctx, req.ID)
		if err != nil {
			return err
		}
		groups, err := c.Store.ListGroups(ctx)
		if err != nil {
			return err
		}
		if req.Name != "" {
			if g.Name, err = cleanGroupName(req.Name); err != nil {
				return err
			}
		}
		if req.Parent != nil {
			p := *req.Parent
			if !groupExists(groups, p) {
				return errcode.Newf(errcode.NotFound, "group %d not found", p)
			}
			if insideGroup(groups, p, g.ID) {
				return errcode.Errorf(errcode.InvalidInput, "a group cannot go inside itself")
			}
			if depth(groups, p)+subtreeHeight(groups, g.ID) > maxGroupDepth {
				return errcode.Newf(errcode.InvalidInput, "groups nest at most %d levels", maxGroupDepth)
			}
			g.ParentID = p
		}
		if err := checkGroupName(groups, g.ParentID, g.Name, g.ID); err != nil {
			return err
		}
		if err := c.Store.UpdateGroup(ctx, g); err != nil {
			return err
		}
		res, err = c.groupInfo(ctx, g.ID)
		return err
	})
	return res, err
}

// insideGroup reports whether id is ancestor itself or one of its subgroups.
func insideGroup(groups []store.Group, id, ancestor int64) bool {
	parent := map[int64]int64{}
	for _, g := range groups {
		parent[g.ID] = g.ParentID
	}
	for i := 0; id != 0 && i <= maxGroupDepth+1; i++ {
		if id == ancestor {
			return true
		}
		id = parent[id]
	}
	return false
}

// subtreeHeight is 1 for a group without subgroups.
func subtreeHeight(groups []store.Group, id int64) int {
	h := 0
	for _, g := range groups {
		if g.ParentID == id {
			h = max(h, subtreeHeight(groups, g.ID))
		}
	}
	return h + 1
}

// RemoveGroup deletes a group. Its sites and subgroups move up to the
// enclosing group; no site is removed.
func (c *Core) RemoveGroup(ctx context.Context, id int64) (GroupInfo, error) {
	var res GroupInfo
	err := c.locked(ctx, func() error {
		var err error
		if res, err = c.groupInfo(ctx, id); err != nil {
			return err
		}
		return c.Store.RemoveGroup(ctx, id)
	})
	return res, err
}

// ResolveGroup finds a group by path ("Clients/Acme", case-insensitive).
// "" and "/" are the top level (0).
func (c *Core) ResolveGroup(ctx context.Context, path string) (int64, error) {
	path = strings.Trim(strings.TrimSpace(path), "/")
	if path == "" {
		return 0, nil
	}
	all, err := c.ListGroups(ctx)
	if err != nil {
		return 0, err
	}
	for _, g := range all {
		if strings.EqualFold(g.Path, path) {
			return g.ID, nil
		}
	}
	return 0, errcode.Newf(errcode.NotFound, "group %q not found (see covo group list)", path)
}

// AddGroupPath creates every missing group along path ("Clients/Acme") and
// returns the last one, like mkdir -p.
func (c *Core) AddGroupPath(ctx context.Context, path string) (GroupInfo, error) {
	parts := strings.Split(strings.Trim(strings.TrimSpace(path), "/"), "/")
	for i := range parts {
		parts[i] = strings.TrimSpace(parts[i])
	}
	if len(parts) == 1 && parts[0] == "" {
		return GroupInfo{}, errcode.Errorf(errcode.InvalidInput, "group path is empty")
	}
	var parent int64
	var res GroupInfo
	for i := range parts {
		sub := strings.Join(parts[:i+1], "/")
		id, err := c.ResolveGroup(ctx, sub)
		if errcode.Code(err) == errcode.NotFound {
			if res, err = c.AddGroup(ctx, parts[i], parent); err != nil {
				return GroupInfo{}, err
			}
			parent = res.ID
			continue
		}
		if err != nil {
			return GroupInfo{}, err
		}
		parent = id
		if res, err = c.groupInfo(ctx, id); err != nil {
			return GroupInfo{}, err
		}
	}
	return res, nil
}

// GroupEnabledResult lists the sites a group switch turned on or off.
type GroupEnabledResult struct {
	Group   GroupInfo `json:"group"`
	Enabled bool      `json:"enabled"`
	// Changed are the sites switched; Archived stay off and are not counted.
	Changed  []string `json:"changed"`
	Archived []string `json:"archived,omitempty"`
	Applied  bool     `json:"applied"`
}

// SetGroupEnabled turns serving on or off for every site in a group and its
// subgroups at once (archived sites stay off). If applying fails, every
// site goes back as it was.
func (c *Core) SetGroupEnabled(ctx context.Context, id int64, enabled bool) (GroupEnabledResult, error) {
	res := GroupEnabledResult{Enabled: enabled, Changed: []string{}}
	err := c.locked(ctx, func() error {
		var err error
		if res.Group, err = c.groupInfo(ctx, id); err != nil {
			return err
		}
		groups, err := c.Store.ListGroups(ctx)
		if err != nil {
			return err
		}
		sites, err := c.Store.ListSites(ctx)
		if err != nil {
			return err
		}
		var olds []store.Site
		for _, s := range sites {
			if s.GroupID == 0 || !insideGroup(groups, s.GroupID, id) {
				continue
			}
			if s.Archived {
				res.Archived = append(res.Archived, s.Name)
				continue
			}
			if s.Enabled == enabled {
				continue
			}
			olds = append(olds, s)
			s.Enabled = enabled
			if err := c.Store.UpdateSite(ctx, s); err != nil {
				for _, o := range olds {
					_ = c.Store.UpdateSite(ctx, o)
				}
				return err
			}
			res.Changed = append(res.Changed, s.Name)
		}
		if len(olds) == 0 {
			_, res.Applied, err = c.Store.Setting(ctx, settingRunning)
			return err
		}
		restore := func() {
			for _, o := range olds {
				_ = c.Store.UpdateSite(ctx, o)
			}
			_ = c.syncLocal(ctx)
			_, _ = c.applyIfRunning(ctx)
		}
		if err := c.syncLocal(ctx); err != nil {
			restore()
			return err
		}
		if res.Applied, err = c.applyIfRunning(ctx); err != nil {
			restore()
			return err
		}
		return nil
	})
	return res, err
}
