package handlers

import (
	"net/http"

	"github.com/ethpandaops/dugtrio/frontend"
)

type SessionsPage struct {
	Sessions     []*SessionsPageSession `json:"sessions"`
	SessionCount uint64                 `json:"session_count"`
}

type SessionsPageSession struct {
	Index     int     `json:"index"`
	Key       string  `json:"key"`
	FirstSeen string  `json:"first_seen"`
	LastSeen  string  `json:"last_seen"`
	Requests  uint64  `json:"requests"`
	Tokens    float64 `json:"tokens"`
	Target    string  `json:"target"`
}

// Sessions will return the "sessions" page using a go template
func (fh *FrontendHandler) Sessions(w http.ResponseWriter, r *http.Request) {
	var templateFiles = append(frontend.LayoutTemplateFiles,
		"sessions/sessions.html",
	)

	var pageTemplate = frontend.GetTemplate(templateFiles...)
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

	// get sessions
	for index, session := range fh.proxy.GetSessions() {
		sessionData := &SessionsPageSession{
			Index:     index + 1,
			Key:       session.GetIpAddr(),
			FirstSeen: session.GetFirstSeen().Format("2006-01-02 15:04:05"),
			LastSeen:  session.GetLastSeen().Format("2006-01-02 15:04:05"),
			Requests:  session.GetRequests(),
			Tokens:    session.GetLimiterTokens(),
			Target:    "",
		}
		if lastClient := session.GetLastPoolClient(); lastClient != nil {
			sessionData.Target = lastClient.GetName()
		}

		pageData.Sessions = append(pageData.Sessions, sessionData)
	}
	pageData.SessionCount = uint64(len(pageData.Sessions))

	return pageData, nil
}
