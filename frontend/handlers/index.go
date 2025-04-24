package handlers

import (
	"net/http"

	"github.com/ethpandaops/dugtrio/frontend"
)

type IndexPage struct {
	ProxyStatus bool   `json:"proxy_status"`
	ClientCount uint64 `json:"client_count"`
	ReadyCount  uint64 `json:"ready_count"`
	HeadSlot    uint64 `json:"head_slot"`
	HeadRoot    []byte `json:"head_root"`
}

// Index will return the "index" page using a go template
func (fh *FrontendHandler) Index(w http.ResponseWriter, r *http.Request) {
	templateFiles := frontend.LayoutTemplateFiles
	templateFiles = append(templateFiles, "index/index.html")
	pageTemplate := frontend.GetTemplate(templateFiles...)
	data := frontend.InitPageData(w, r, "index", "/", "Index", templateFiles)

	var pageError error

	data.Data, pageError = fh.getIndexPageData()
	if pageError != nil {
		frontend.HandlePageError(w, r, pageError)
		return
	}

	w.Header().Set("Content-Type", "text/html")

	if frontend.HandleTemplateError(w, r, "index.go", "Index", "", pageTemplate.ExecuteTemplate(w, "layout", data)) != nil {
		return // an error has occurred and was processed
	}
}

func (fh *FrontendHandler) getIndexPageData() (*IndexPage, error) {
	pageData := &IndexPage{}

	allClients := fh.pool.GetAllEndpoints()
	pageData.ClientCount = uint64(len(allClients))

	canonicalFork := fh.pool.GetCanonicalFork()
	if canonicalFork != nil {
		pageData.ReadyCount = uint64(len(canonicalFork.ReadyClients))
		pageData.ProxyStatus = pageData.ReadyCount > 0
		pageData.HeadSlot = uint64(canonicalFork.Slot)
		pageData.HeadRoot = canonicalFork.Root[:]
	}

	return pageData, nil
}
