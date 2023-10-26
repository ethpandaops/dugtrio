package handlers

import (
	"net/http"
	"time"

	"github.com/ethpandaops/dugtrio/frontend"
	"github.com/ethpandaops/dugtrio/pool"
)

type HealthPage struct {
	Clients     []*HealthPageClient `json:"clients"`
	ClientCount uint64              `json:"client_count"`
}

type HealthPageClient struct {
	Index       int       `json:"index"`
	Name        string    `json:"name"`
	Version     string    `json:"version"`
	HeadSlot    uint64    `json:"head_slot"`
	HeadRoot    []byte    `json:"head_root"`
	Status      string    `json:"status"`
	LastRefresh time.Time `json:"refresh"`
	LastError   string    `json:"error"`
}

// Health will return the "health" page using a go template
func (fh *FrontendHandler) Health(w http.ResponseWriter, r *http.Request) {
	var templateFiles = append(frontend.LayoutTemplateFiles,
		"health/health.html",
	)

	var pageTemplate = frontend.GetTemplate(templateFiles...)
	data := frontend.InitPageData(w, r, "health", "/health", "Health", templateFiles)

	var pageError error
	data.Data, pageError = fh.getHealthPageData()
	if pageError != nil {
		frontend.HandlePageError(w, r, pageError)
		return
	}
	w.Header().Set("Content-Type", "text/html")
	if frontend.HandleTemplateError(w, r, "health.go", "Health", "", pageTemplate.ExecuteTemplate(w, "layout", data)) != nil {
		return // an error has occurred and was processed
	}
}

func (fh *FrontendHandler) getHealthPageData() (*HealthPage, error) {
	pageData := &HealthPage{
		Clients: []*HealthPageClient{},
	}

	for _, client := range fh.pool.GetAllEndpoints() {
		headSlot, headRoot := client.GetLastHead()
		clientData := &HealthPageClient{
			Index:       int(client.GetIndex()),
			Name:        client.GetName(),
			Version:     client.GetVersion(),
			HeadSlot:    uint64(headSlot),
			HeadRoot:    headRoot[:],
			LastRefresh: client.GetLastEventTime(),
		}
		if lastError := client.GetLastError(); lastError != nil {
			clientData.LastError = lastError.Error()
		}
		switch client.GetStatus() {
		case pool.ClientStatusOffline:
			clientData.Status = "offline"
		case pool.ClientStatusOnline:
			clientData.Status = "online"
		case pool.ClientStatusOptimistic:
			clientData.Status = "optimistic"
		case pool.ClientStatusSynchronizing:
			clientData.Status = "synchronizing"
		}
		pageData.Clients = append(pageData.Clients, clientData)
	}
	pageData.ClientCount = uint64(len(pageData.Clients))

	return pageData, nil
}
