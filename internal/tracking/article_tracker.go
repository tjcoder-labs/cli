package tracking

import (
	"fmt"
	"strings"

	"github.com/tjcoder-labs/coder-cli/internal/articles"
	"github.com/tjcoder-labs/coder-cli/internal/session"
)

// ArticleTracker adapts articles.Store to the generic Tracker
// interface.
type ArticleTracker struct {
	store *articles.Store
}

func NewArticleTracker() *ArticleTracker {
	return &ArticleTracker{store: articles.NewStore()}
}

func (t *ArticleTracker) Type() string { return "article" }

func (t *ArticleTracker) List(state session.State) []interface{} {
	list := t.store.List(state)
	all := list.All()
	out := make([]interface{}, len(all))
	for i := range all {
		out[i] = all[i]
	}
	return out
}

func (t *ArticleTracker) Get(state session.State, id string) interface{} {
	list := t.store.List(state)
	a, ok := list.Get(id)
	if !ok {
		return nil
	}
	return a
}

func (t *ArticleTracker) Create(state session.State, input map[string]interface{}) (session.State, interface{}, error) {
	title, _ := input["title"].(string)
	if strings.TrimSpace(title) == "" {
		return state, nil, fmt.Errorf("title is required")
	}
	body, _ := input["body"].(string)
	source, _ := input["source"].(string)
	tags := []string{}
	if raw, ok := input["tags"].([]interface{}); ok {
		for _, v := range raw {
			if s, ok := v.(string); ok && s != "" {
				tags = append(tags, s)
			}
		}
	}
	res, err := t.store.Create(state, articles.CreateInput{Title: title, Body: body, Tags: tags, Source: source})
	if err != nil {
		return state, nil, err
	}
	return res.State, res.Article, nil
}

func (t *ArticleTracker) Update(state session.State, id string, input map[string]interface{}) (session.State, interface{}, error) {
	in := articles.UpdateInput{ID: id}
	if v, ok := input["title"].(string); ok {
		in.Title = v
	}
	if v, ok := input["body"].(string); ok {
		in.Body = v
	}
	if v, ok := input["source"].(string); ok {
		in.Source = v
	}
	if raw, ok := input["tags"].([]interface{}); ok {
		tagStrs := make([]string, 0, len(raw))
		for _, v := range raw {
			if s, ok := v.(string); ok && s != "" {
				tagStrs = append(tagStrs, s)
			}
		}
		in.Tags = tagStrs
	}
	res, err := t.store.Update(state, in)
	if err != nil {
		return state, nil, err
	}
	if !res.OK {
		return state, nil, fmt.Errorf("article not found")
	}
	return res.State, res.Article, nil
}

func (t *ArticleTracker) Delete(state session.State, id string) (session.State, bool, error) {
	newState, ok := t.store.Delete(state, strings.TrimSpace(id))
	return newState, ok, nil
}

func (t *ArticleTracker) Format(obj interface{}) string {
	if a, ok := obj.(articles.Article); ok {
		return fmt.Sprintf("[%s] %s (%s)", a.Source, a.Title, a.ID)
	}
	return fmt.Sprintf("%v", obj)
}
