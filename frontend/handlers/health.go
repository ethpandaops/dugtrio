package handlers

import (
	"net/http"
	"sort"
	"time"

	"github.com/ethpandaops/dugtrio/frontend"
	"github.com/ethpandaops/dugtrio/pool"
)

type HealthPage struct {
	Clients     []*HealthPageClient `json:"clients"`
	ClientCount uint64              `json:"client_count"`

	Blocks     []*HealthPageBlock `json:"blocks"`
	BlockCount uint64             `json:"block_count"`

	Forks     []*HealthPageFork `json:"forks"`
	ForkCount uint64            `json:"fork_count"`
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
	IsReady     bool      `json:"ready"`
}

type HealthPageBlock struct {
	Slot   uint64   `json:"slot"`
	Root   []byte   `json:"root"`
	SeenBy []string `json:"seen_by"`
}

type HealthPageFork struct {
	HeadSlot    uint64                  `json:"head_slot"`
	HeadRoot    []byte                  `json:"head_root"`
	Clients     []*HealthPageForkClient `json:"clients"`
	ClientCount uint64                  `json:"client_count"`
}

type HealthPageForkClient struct {
	Client   *HealthPageClient `json:"client"`
	Distance uint64            `json:"distance"`
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

	// get clients
	for _, client := range fh.pool.GetAllEndpoints() {
		clientData := fh.getHealthPageClientData(client)
		pageData.Clients = append(pageData.Clients, clientData)
	}
	pageData.ClientCount = uint64(len(pageData.Clients))

	// get blocks
	for _, block := range fh.pool.GetBlockCache().GetCachedBlocks() {

		blockData := &HealthPageBlock{
			Slot:   uint64(block.Slot),
			Root:   block.Root[:],
			SeenBy: []string{},
		}
		for _, client := range block.GetSeenBy() {
			blockData.SeenBy = append(blockData.SeenBy, client.GetName())
		}

		pageData.Blocks = append(pageData.Blocks, blockData)
	}
	pageData.BlockCount = uint64(len(pageData.Blocks))

	// get forks
	for _, fork := range fh.pool.GetHeadForks() {
		if fork == nil {
			continue
		}
		forkData := &HealthPageFork{
			HeadSlot: uint64(fork.Slot),
			HeadRoot: fork.Root[:],
			Clients:  []*HealthPageForkClient{},
		}
		pageData.Forks = append(pageData.Forks, forkData)

		for _, client := range fork.AllClients {
			_, clientHeadRoot := client.GetLastHead()
			_, forkDistance := fh.pool.GetBlockCache().GetBlockDistance(clientHeadRoot, fork.Root)
			forkClient := &HealthPageForkClient{
				Client:   fh.getHealthPageClientData(client),
				Distance: forkDistance,
			}
			forkData.Clients = append(forkData.Clients, forkClient)
		}
		sort.Slice(forkData.Clients, func(a, b int) bool {
			return forkData.Clients[a].Client.Index < forkData.Clients[b].Client.Index
		})
		forkData.ClientCount = uint64(len(forkData.Clients))
	}
	pageData.ForkCount = uint64(len(pageData.Forks))

	return pageData, nil
}

func (fh *FrontendHandler) getHealthPageClientData(client *pool.PoolClient) *HealthPageClient {
	headSlot, headRoot := client.GetLastHead()
	clientData := &HealthPageClient{
		Index:       int(client.GetIndex()),
		Name:        client.GetName(),
		Version:     client.GetVersion(),
		HeadSlot:    uint64(headSlot),
		HeadRoot:    headRoot[:],
		LastRefresh: client.GetLastEventTime(),
		IsReady:     fh.pool.GetCanonicalFork().IsClientReady(client),
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
	return clientData
}
