package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"html/template"
	"log"
	"net/http"
	"os"
	"os/exec"
	"sort"
	"strings"
	"sync"
	"time"
)

type config struct {
	Port         string
	GitHubToken  string
	GitHubOwner  string
	GitHubRepo   string
	WorkflowFile string
	WorkflowRef  string
	PortalApp    string
	Namespace    string
	IngressName  string
}

type deployRequest struct {
	ReportName string `json:"reportName"`
}

type deployResponse struct {
	RequestID   string `json:"requestId"`
	WorkflowURL string `json:"workflowUrl"`
}

type deploymentState struct {
	RequestedAt time.Time
}

type workflowDispatchBody struct {
	Ref    string            `json:"ref"`
	Inputs map[string]string `json:"inputs"`
}

type workflowRunsResponse struct {
	WorkflowRuns []workflowRun `json:"workflow_runs"`
}

type workflowRun struct {
	ID         int64     `json:"id"`
	Name       string    `json:"name"`
	Status     string    `json:"status"`
	Conclusion string    `json:"conclusion"`
	HTMLURL    string    `json:"html_url"`
	CreatedAt  time.Time `json:"created_at"`
	UpdatedAt  time.Time `json:"updated_at"`
}

type statusResponse struct {
	Status     string `json:"status"`
	Conclusion string `json:"conclusion"`
	RunURL     string `json:"runUrl"`
	PublicURL  string `json:"publicUrl"`
}

var (
	indexTemplate = template.Must(template.New("index").Parse(indexHTML))
	requestsMu    sync.Mutex
	requests      = map[string]deploymentState{}
)

func main() {
	cfg := config{
		Port:         env("PORT", "9090"),
		GitHubToken:  env("GITHUB_TOKEN", ""),
		GitHubOwner:  env("GITHUB_OWNER", "dsahu1001-git"),
		GitHubRepo:   env("GITHUB_REPO", "sample-platform-app"),
		WorkflowFile: env("GITHUB_WORKFLOW_FILE", "day4-gitops-multi-app.yml"),
		WorkflowRef:  env("GITHUB_WORKFLOW_REF", "main"),
		PortalApp:    env("PORTAL_APP_NAME", "app-a"),
		Namespace:    env("KUBE_NAMESPACE", "platform-demo"),
		IngressName:  env("KUBE_INGRESS_NAME", "app-a"),
	}

	http.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if err := indexTemplate.Execute(w, cfg); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
		}
	})
	http.HandleFunc("/api/deploy", withJSON(func(w http.ResponseWriter, r *http.Request) error {
		if cfg.GitHubToken == "" {
			return apiError{Code: http.StatusBadRequest, Message: "set GITHUB_TOKEN before starting the portal"}
		}

		var req deployRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			return apiError{Code: http.StatusBadRequest, Message: "invalid request body"}
		}

		reportName := sanitizeReportName(req.ReportName)
		requestID := fmt.Sprintf("%d", time.Now().UnixNano())
		requestsMu.Lock()
		requests[requestID] = deploymentState{RequestedAt: time.Now().UTC()}
		requestsMu.Unlock()

		if err := triggerWorkflow(cfg, reportName); err != nil {
			return err
		}

		writeJSON(w, http.StatusAccepted, deployResponse{
			RequestID:   requestID,
			WorkflowURL: fmt.Sprintf("https://github.com/%s/%s/actions/workflows/%s", cfg.GitHubOwner, cfg.GitHubRepo, cfg.WorkflowFile),
		})
		return nil
	}))
	http.HandleFunc("/api/status", withJSON(func(w http.ResponseWriter, r *http.Request) error {
		requestID := r.URL.Query().Get("requestId")
		if requestID == "" {
			return apiError{Code: http.StatusBadRequest, Message: "requestId is required"}
		}

		requestsMu.Lock()
		state, ok := requests[requestID]
		requestsMu.Unlock()
		if !ok {
			return apiError{Code: http.StatusNotFound, Message: "request not found"}
		}

		run, err := latestRunAfter(cfg, state.RequestedAt)
		if err != nil {
			return err
		}

		publicURL := ""
		if run.Status == "completed" && run.Conclusion == "success" {
			publicURL = currentPublicURL(cfg)
		}

		writeJSON(w, http.StatusOK, statusResponse{
			Status:     run.Status,
			Conclusion: run.Conclusion,
			RunURL:     run.HTMLURL,
			PublicURL:  publicURL,
		})
		return nil
	}))

	log.Printf("portal listening on http://localhost:%s", cfg.Port)
	log.Fatal(http.ListenAndServe(":"+cfg.Port, nil))
}

func triggerWorkflow(cfg config, reportName string) error {
	body := workflowDispatchBody{
		Ref: cfg.WorkflowRef,
		Inputs: map[string]string{
			"scope":        "single-app",
			"app_name":     cfg.PortalApp,
			"report_name":  reportName,
			"release_ring": "dev",
		},
	}
	payload, err := json.Marshal(body)
	if err != nil {
		return err
	}

	req, err := http.NewRequest(
		http.MethodPost,
		fmt.Sprintf("https://api.github.com/repos/%s/%s/actions/workflows/%s/dispatches", cfg.GitHubOwner, cfg.GitHubRepo, cfg.WorkflowFile),
		bytes.NewReader(payload),
	)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+cfg.GitHubToken)
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusNoContent {
		return apiError{Code: http.StatusBadGateway, Message: "workflow dispatch was not accepted"}
	}
	return nil
}

func latestRunAfter(cfg config, requestedAt time.Time) (workflowRun, error) {
	req, err := http.NewRequest(
		http.MethodGet,
		fmt.Sprintf("https://api.github.com/repos/%s/%s/actions/workflows/%s/runs?branch=%s&event=workflow_dispatch&per_page=10", cfg.GitHubOwner, cfg.GitHubRepo, cfg.WorkflowFile, cfg.WorkflowRef),
		nil,
	)
	if err != nil {
		return workflowRun{}, err
	}
	req.Header.Set("Authorization", "Bearer "+cfg.GitHubToken)
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return workflowRun{}, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return workflowRun{}, apiError{Code: http.StatusBadGateway, Message: "could not read workflow runs"}
	}

	var runs workflowRunsResponse
	if err := json.NewDecoder(resp.Body).Decode(&runs); err != nil {
		return workflowRun{}, err
	}

	sort.Slice(runs.WorkflowRuns, func(i, j int) bool {
		return runs.WorkflowRuns[i].CreatedAt.After(runs.WorkflowRuns[j].CreatedAt)
	})

	for _, run := range runs.WorkflowRuns {
		if run.CreatedAt.After(requestedAt.Add(-5 * time.Second)) {
			return run, nil
		}
	}

	return workflowRun{
		Status:  "queued",
		HTMLURL: fmt.Sprintf("https://github.com/%s/%s/actions/workflows/%s", cfg.GitHubOwner, cfg.GitHubRepo, cfg.WorkflowFile),
	}, nil
}

func currentPublicURL(cfg config) string {
	cmd := exec.Command("kubectl", "-n", cfg.Namespace, "get", "ingress", cfg.IngressName, "-o", "jsonpath={.status.loadBalancer.ingress[0].hostname}")
	output, err := cmd.Output()
	if err != nil {
		return ""
	}
	host := strings.TrimSpace(string(output))
	if host == "" {
		return ""
	}
	return "http://" + host + "/"
}

func sanitizeReportName(value string) string {
	clean := strings.Map(func(r rune) rune {
		switch {
		case r >= 'a' && r <= 'z':
			return r
		case r >= 'A' && r <= 'Z':
			return r
		case r >= '0' && r <= '9':
			return r
		case r == '-' || r == '_':
			return r
		default:
			return -1
		}
	}, value)
	if clean == "" {
		return "platform-demo"
	}
	if len(clean) > 40 {
		return clean[:40]
	}
	return clean
}

func env(key, fallback string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return fallback
}

type apiError struct {
	Code    int
	Message string
}

func (err apiError) Error() string {
	return err.Message
}

func withJSON(next func(http.ResponseWriter, *http.Request) error) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if err := next(w, r); err != nil {
			if apiErr, ok := err.(apiError); ok {
				writeJSON(w, apiErr.Code, map[string]string{"error": apiErr.Message})
				return
			}
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		}
	}
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}

const indexHTML = `<!doctype html>
<html lang="en">
  <head>
    <meta charset="utf-8" />
    <meta name="viewport" content="width=device-width, initial-scale=1" />
    <title>Platform Launchpad</title>
    <style>
      :root {
        --bg: #f4efe4;
        --ink: #142033;
        --card: rgba(255,255,255,0.76);
        --line: rgba(20,32,51,0.12);
        --teal: #0f8a7b;
        --orange: #d96a18;
        --soft: #6b7280;
      }
      * { box-sizing: border-box; }
      body {
        margin: 0;
        font-family: Georgia, "Times New Roman", serif;
        background:
          radial-gradient(circle at top left, rgba(15,138,123,0.14), transparent 32%),
          radial-gradient(circle at bottom right, rgba(217,106,24,0.14), transparent 34%),
          var(--bg);
        color: var(--ink);
      }
      .shell {
        max-width: 1040px;
        margin: 0 auto;
        padding: 40px 20px 64px;
      }
      .hero {
        display: grid;
        gap: 24px;
        margin-bottom: 28px;
      }
      h1 {
        margin: 0;
        font-size: clamp(2.3rem, 6vw, 4rem);
        line-height: 0.95;
      }
      p.lead {
        max-width: 720px;
        margin: 0;
        font-size: 1.08rem;
        line-height: 1.6;
        color: rgba(20,32,51,0.82);
      }
      .grid {
        display: grid;
        gap: 20px;
        grid-template-columns: 1.1fr 0.9fr;
      }
      .panel {
        background: var(--card);
        border: 1px solid var(--line);
        border-radius: 10px;
        padding: 22px;
        backdrop-filter: blur(12px);
      }
      .eyebrow {
        letter-spacing: 0.08em;
        text-transform: uppercase;
        font-size: 0.78rem;
        color: var(--soft);
        margin-bottom: 10px;
      }
      .stat {
        display: grid;
        gap: 8px;
        padding: 14px 0;
        border-top: 1px solid var(--line);
      }
      .stat:first-of-type { border-top: 0; padding-top: 0; }
      label { display: block; font-size: 0.95rem; margin-bottom: 8px; }
      input {
        width: 100%;
        padding: 13px 14px;
        border-radius: 8px;
        border: 1px solid rgba(20,32,51,0.16);
        font: inherit;
        background: rgba(255,255,255,0.92);
      }
      button {
        margin-top: 14px;
        width: 100%;
        border: 0;
        border-radius: 8px;
        padding: 14px 16px;
        font: inherit;
        color: white;
        background: linear-gradient(135deg, var(--teal), var(--orange));
        cursor: pointer;
      }
      code {
        font-family: "SFMono-Regular", Consolas, monospace;
        font-size: 0.9rem;
      }
      .status {
        white-space: pre-wrap;
        line-height: 1.6;
        color: rgba(20,32,51,0.88);
      }
      @media (max-width: 820px) {
        .grid { grid-template-columns: 1fr; }
      }
    </style>
  </head>
  <body>
    <div class="shell">
      <div class="hero">
        <div class="eyebrow">Day 4 Platform Portal</div>
        <h1>Platform Launchpad</h1>
        <p class="lead">Give the platform a report name and let the workflow hide the moving parts: build, image push, GitOps update, Argo sync, and the resulting ALB URL.</p>
      </div>
      <div class="grid">
        <section class="panel">
          <div class="eyebrow">Launch Request</div>
          <form id="deploy-form">
            <label for="report-name">Report name</label>
            <input id="report-name" name="report-name" value="day4-demo" />
            <button type="submit">Deploy Portal App</button>
          </form>
        </section>
        <section class="panel">
          <div class="eyebrow">Current Wiring</div>
          <div class="stat"><strong>Workflow</strong><code>{{ .WorkflowFile }}</code></div>
          <div class="stat"><strong>Target App</strong><code>{{ .PortalApp }}</code></div>
          <div class="stat"><strong>Ingress</strong><code>{{ .IngressName }}</code></div>
          <div class="stat"><strong>Namespace</strong><code>{{ .Namespace }}</code></div>
        </section>
      </div>
      <section class="panel" style="margin-top:20px;">
        <div class="eyebrow">Deployment Status</div>
        <div id="status" class="status">Submit a report name to trigger the GitHub Actions workflow.</div>
      </section>
    </div>
    <script>
      const form = document.getElementById("deploy-form");
      const statusNode = document.getElementById("status");
      let timer = null;

      async function pollStatus(requestId) {
        const response = await fetch("/api/status?requestId=" + encodeURIComponent(requestId));
        const data = await response.json();
        statusNode.textContent =
          "Status: " + data.status + "\n" +
          "Conclusion: " + (data.conclusion || "pending") + "\n" +
          "Run: " + (data.runUrl || "waiting") + "\n" +
          "Public URL: " + (data.publicUrl || "not ready");
        if (data.status !== "completed") {
          timer = setTimeout(() => pollStatus(requestId), 5000);
        }
      }

      form.addEventListener("submit", async (event) => {
        event.preventDefault();
        clearTimeout(timer);
        statusNode.textContent = "Triggering workflow...";
        const reportName = document.getElementById("report-name").value;
        const response = await fetch("/api/deploy", {
          method: "POST",
          headers: { "Content-Type": "application/json" },
          body: JSON.stringify({ reportName })
        });
        const data = await response.json();
        if (!response.ok) {
          statusNode.textContent = data.error || "Could not start deployment";
          return;
        }
        statusNode.textContent = "Workflow requested.\nRun list: " + data.workflowUrl;
        pollStatus(data.requestId);
      });
    </script>
  </body>
</html>`
