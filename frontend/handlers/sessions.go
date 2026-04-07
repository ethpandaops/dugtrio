package handlers

import (
	"fmt"
	"net/http"

	"github.com/ethpandaops/dugtrio/frontend"
	"github.com/ethpandaops/dugtrio/pool"
)

type SessionsPage struct {
	Sessions     []*SessionsPageSession `json:"sessions"`
	SessionCount uint64                 `json:"session_count"`
}

type SessionsPageSession struct {
	Index     int                          `json:"index"`
	Key       string                       `json:"key"`
	FirstSeen string                       `json:"first_seen"`
	LastSeen  string                       `json:"last_seen"`
	Requests  uint64                       `json:"requests"`
	Tokens    float64                      `json:"tokens"`
	Target    string                       `json:"target"`
	Targets   []*SessionsPageSessionTarget `json:"targets"`
}

type SessionsPageSessionTarget struct {
	Prefix string `json:"prefix"`
	Target string `json:"target"`
}

// Sessions will return the "sessions" page using a go template
func (fh *FrontendHandler) Sessions(w http.ResponseWriter, r *http.Request) {
	templateFiles := frontend.LayoutTemplateFiles
	templateFiles = append(templateFiles, "sessions/sessions.html")
	pageTemplate := frontend.GetTemplate(templateFiles...)
	data := frontend.InitPageData(w, r, "sessions", "/sessions", "Sessions", templateFiles)

	var pageError error

	data.Data, pageError = fh.getSessionsPageData()
	if pageError != nil {
		frontend.HandlePageError(w, r, pageError)
		return
	}

	w.Header().Set("Content-Type", "text/html")

	if frontend.HandleTemplateError(w, r, "sessions.go", "Sessions", "", pageTemplate.ExecuteTemplate(w, "layout", data)) != nil {
		return // an error has occurred and was processed
	}
}

func (fh *FrontendHandler) getSessionsPageData() (*SessionsPage, error) {
	pageData := &SessionsPage{
		Sessions: []*SessionsPageSession{},
	}

	for index, group := range fh.proxy.GetSessionGroups() {
		sessionData := &SessionsPageSession{
			Index:     index + 1,
			Key:       group.GetIPAddr(),
			FirstSeen: group.GetFirstSeen().Format("2006-01-02 15:04:05"),
			LastSeen:  group.GetLastSeen().Format("2006-01-02 15:04:05"),
			Requests:  group.GetRequests(),
			Tokens:    group.GetLimiterTokens(),
			Target:    "",
			Targets:   []*SessionsPageSessionTarget{},
		}

		for _, session := range group.GetSessions() {
			prefix := "main"
			if session.GetPrefix() != pool.UnspecifiedClient {
				prefix = session.GetPrefix().String()
			}

			target := ""
			if lastClient := session.GetLastPoolClient(); lastClient != nil {
				target = lastClient.GetName()
			}

			if sessionData.Target != "" {
				sessionData.Target += ", "
			}

			sessionData.Target += fmt.Sprintf("%s: %s", prefix, target)

			sessionData.Targets = append(sessionData.Targets, &SessionsPageSessionTarget{
				Prefix: prefix,
				Target: target,
			})
		}

		pageData.Sessions = append(pageData.Sessions, sessionData)
	}

	pageData.SessionCount = uint64(len(pageData.Sessions))

	return pageData, nil
}
