package handlers

import (
	"net/http"

	"github.com/ethpandaops/dugtrio/frontend"
)

// Health will return the "health" page using a go template
func Health(w http.ResponseWriter, r *http.Request) {
	var templateFiles = append(frontend.LayoutTemplateFiles,
		"health/health.html",
	)

	var pageTemplate = frontend.GetTemplate(templateFiles...)
	data := frontend.InitPageData(w, r, "health", "/health", "Health", templateFiles)

	var pageError error

	if pageError != nil {
		frontend.HandlePageError(w, r, pageError)
		return
	}
	w.Header().Set("Content-Type", "text/html")
	if frontend.HandleTemplateError(w, r, "health.go", "Health", "", pageTemplate.ExecuteTemplate(w, "layout", data)) != nil {
		return // an error has occurred and was processed
	}
}
