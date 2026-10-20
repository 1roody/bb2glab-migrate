package gitlab

import "encoding/json"

// Project: campos do projeto no GitLab usados pela migração.
type Project struct {
	ID                int    `json:"id"`
	Name              string `json:"name"`
	Path              string `json:"path"`
	PathWithNamespace string `json:"path_with_namespace"`
	HTTPURLToRepo     string `json:"http_url_to_repo"`
	WebURL            string `json:"web_url"`
	ImportStatus      string `json:"import_status"`
	ImportType        string `json:"import_type"`
	ImportError       string `json:"import_error"`
	EmptyRepo         bool   `json:"empty_repo"`
	LastActivityAt    string `json:"last_activity_at"`
}

// ImportedWithPRs: projeto criado pelo importador do Bitbucket Cloud, com a importação concluída.
func (p Project) ImportedWithPRs() bool {
	return p.ImportType == "bitbucket" && p.ImportStatus == "finished"
}

func (p Project) ImportRunning() bool {
	return p.ImportStatus == "scheduled" || p.ImportStatus == "started"
}

// ImportInfo: GET projects/:id/import.
type ImportInfo struct {
	ImportStatus    string            `json:"import_status"`
	ImportError     string            `json:"import_error"`
	FailedRelations []json.RawMessage `json:"failed_relations"`
}
