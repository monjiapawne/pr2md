package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"net/http"
	"os"
	"regexp"
	"text/template"
	"time"

	"go.yaml.in/yaml/v2"
)

const (
	configPath   = "config.yml"
	templatePath = "templates/*.tmpl"
)

type cfg struct {
	jsonEnabled bool
	mdEnabled   bool
}

func main() {
	mdEnabled := flag.Bool("md-hide", false, "toggle to disable markdown output")
	jsonEnabled := flag.Bool("json", false, "toggle to enable json output")
	flag.Parse()

	cfg := cfg{
		jsonEnabled: *jsonEnabled,
		mdEnabled:   !*mdEnabled, // invert (default true)
	}
	if err := Run(cfg); err != nil {
		fmt.Println("error", err)
	}
}

var issueRefRe = regexp.MustCompile(`(?i)(?:closes?|fixes?|resolves?)\s+#(\d+)`)

func extractIssues(body string) []string {
	matches := issueRefRe.FindAllStringSubmatch(body, -1)
	issues := make([]string, 0, len(matches))
	for _, m := range matches {
		issues = append(issues, m[1])
	}
	return issues
}

type PRData struct {
	// PR
	Title     string    `json:"title"`
	Body      string    `json:"body"`
	Issues    []string  `json:"issues,omitempty"`
	HTMLURL   string    `json:"html_url"`
	Additions int       `json:"additions"`
	Deletions int       `json:"deletions"`
	MergedAt  time.Time `json:"merged_at"`

	// Repo
	Base struct {
		Repo struct {
			Name        string `json:"name"`
			Description string `json:"description"`
			URL         string `json:"url"`
			Language    string `json:"language"`
			Stars       int    `json:"stargazers_count"`
		} `json:"repo"`
	} `json:"base"`

	// Ancillary
	CommentURL string `json:"comments_url"`
	Comments   []struct {
		Comment   string `json:"-"`
		UserLogin string `json:"-"`
	} `json:"-"`
}

func (p *PRData) UnmarshalJSON(data []byte) error {
	type Alias PRData
	if err := json.Unmarshal(data, (*Alias)(p)); err != nil {
		return err
	}
	p.Issues = extractIssues(p.Body)
	return nil
}

type Config struct {
	Header        string        `yaml:"header"`
	Contributions Contributions `yaml:"contributions"`
}

type Contribution struct {
	Note string `yaml:"note"`
}
type Contributions []map[string]Contribution

func FetchPR(url string) (PRData, error) {
	var pr PRData

	resp, err := http.Get(url)
	if err != nil {
		return pr, err
	}
	defer resp.Body.Close()

	if err := json.NewDecoder(resp.Body).Decode(&pr); err != nil {
		return pr, err
	}

	respComments, err := http.Get(pr.CommentURL)
	if err != nil {
		return pr, err
	}
	defer respComments.Body.Close()

	var rawComments []struct {
		Body string `json:"body"`
		User struct {
			Login string `json:"login"`
		} `json:"user"`
	}
	if err := json.NewDecoder(respComments.Body).Decode(&rawComments); err != nil {
		return pr, err
	}
	for _, c := range rawComments {
		pr.Comments = append(pr.Comments, struct {
			Comment   string `json:"-"`
			UserLogin string `json:"-"`
		}{
			Comment:   c.Body,
			UserLogin: c.User.Login,
		})
	}

	return pr, nil
}

type RenderData struct {
	Header string
	PRs    map[string]PRData
}

func Render(cfg cfg, header string, prs map[string]PRData) error {
	data := RenderData{Header: header, PRs: prs}
	var tmpl *template.Template
	if cfg.mdEnabled {
		var err error
		tmpl, err = template.New("").Funcs(template.FuncMap{
			"dateFormat": func(t time.Time) string {
				return t.Format("January 2, 2006")
			},
		}).ParseGlob(templatePath)
		if err != nil {
			return err
		}
	}

	if cfg.jsonEnabled {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		if err := enc.Encode(prs); err != nil {
			return err
		}
	}

	if cfg.mdEnabled {
		if err := tmpl.ExecuteTemplate(os.Stdout, "base", data); err != nil {
			return err
		}
	}

	return nil
}

func Run(cfg cfg) error {
	file, err := os.Open(configPath)
	if err != nil {
		return err
	}
	defer file.Close()

	var config Config
	if err := yaml.NewDecoder(file).Decode(&config); err != nil {
		return err
	}

	prs := make(map[string]PRData)
	for _, entry := range config.Contributions {
		for url, meta := range entry {
			pr, err := FetchPR(url)
			if err != nil {
				return err
			}
			_ = meta
			prs[url] = pr
		}
	}
	return Render(cfg, config.Header, prs)
}
