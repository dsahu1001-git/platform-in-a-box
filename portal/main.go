package main

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"html/template"
	"io"
	"log"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

type config struct {
	Port             string
	GitHubToken      string
	DefaultOwner     string
	DefaultRepo      string
	DefaultWorkflow  string
	DefaultRef       string
	DefaultScope     string
	DefaultApp       string
	DefaultRing      string
	DefaultNamespace string
	PrometheusURL    string
	GrafanaURL       string
}

type dispatchTarget struct {
	Owner        string `json:"owner"`
	Repo         string `json:"repo"`
	WorkflowFile string `json:"workflowFile"`
	WorkflowRef  string `json:"workflowRef"`
	Scope        string `json:"scope"`
	AppName      string `json:"appName"`
	ReleaseRing  string `json:"releaseRing"`
	Namespace    string `json:"namespace"`
}

type deployRequest struct {
	ReportName string         `json:"reportName"`
	Target     dispatchTarget `json:"target"`
}

type deployResponse struct {
	RequestID   string         `json:"requestId"`
	WorkflowURL string         `json:"workflowUrl"`
	Target      dispatchTarget `json:"target"`
	Skipped     bool           `json:"skipped"`
	Message     string         `json:"message,omitempty"`
}

type deploymentState struct {
	RequestedAt time.Time
	Target      dispatchTarget
	ReportName  string
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
	Event      string    `json:"event"`
	RunNumber  int       `json:"run_number"`
	RunAttempt int       `json:"run_attempt"`
	CreatedAt  time.Time `json:"created_at"`
	UpdatedAt  time.Time `json:"updated_at"`
}

type statusResponse struct {
	Status     string         `json:"status"`
	Conclusion string         `json:"conclusion"`
	RunURL     string         `json:"runUrl"`
	RunID      int64          `json:"runId"`
	RunNumber  int            `json:"runNumber"`
	RunAttempt int            `json:"runAttempt"`
	CreatedAt  string         `json:"createdAt"`
	UpdatedAt  string         `json:"updatedAt"`
	Target     dispatchTarget `json:"target"`
	ReportName string         `json:"reportName"`
}

type checkItem struct {
	Name    string `json:"name"`
	Status  string `json:"status"`
	Detail  string `json:"detail"`
	Command string `json:"command,omitempty"`
}

type overviewResponse struct {
	GeneratedAt string      `json:"generatedAt"`
	Checks      []checkItem `json:"checks"`
}

type githubRepoResponse struct {
	FullName string `json:"full_name"`
	Private  bool   `json:"private"`
}

type apiError struct {
	Code    int
	Message string
}

func (err apiError) Error() string {
	return err.Message
}

var (
	indexTemplate = template.Must(template.New("index").Parse(indexHTML))
	requestsMu    sync.Mutex
	requests      = map[string]deploymentState{}
)

func main() {
	cfg := config{
		Port:             env("PORT", "9090"),
		GitHubToken:      env("GITHUB_TOKEN", ""),
		DefaultOwner:     env("GITHUB_OWNER", "dsahu1001-git"),
		DefaultRepo:      env("GITHUB_REPO", "sample-platform-app"),
		DefaultWorkflow:  env("GITHUB_WORKFLOW_FILE", "day4-gitops-multi-app.yml"),
		DefaultRef:       env("GITHUB_WORKFLOW_REF", "main"),
		DefaultScope:     env("PORTAL_SCOPE", "single-app"),
		DefaultApp:       env("PORTAL_APP_NAME", "app-a"),
		DefaultRing:      env("PORTAL_RELEASE_RING", "dev"),
		DefaultNamespace: env("KUBE_NAMESPACE", "platform-demo"),
		PrometheusURL:    env("PROMETHEUS_URL", "http://127.0.0.1:9090"),
		GrafanaURL:       env("GRAFANA_URL", "http://127.0.0.1:3000"),
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

		target := normalizedTarget(cfg, req.Target)
		reportName := sanitizeReportName(req.ReportName)

		changed, reason, err := hasMeaningfulChange(cfg.GitHubToken, target, reportName)
		if err != nil {
			return err
		}
		if !changed {
			writeJSON(w, http.StatusOK, deployResponse{
				RequestID:   "",
				WorkflowURL: workflowURL(target),
				Target:      target,
				Skipped:     true,
				Message:     reason,
			})
			return nil
		}

		requestID := fmt.Sprintf("%d", time.Now().UnixNano())
		requestsMu.Lock()
		requests[requestID] = deploymentState{
			RequestedAt: time.Now().UTC(),
			Target:      target,
			ReportName:  reportName,
		}
		requestsMu.Unlock()

		if err := triggerWorkflow(cfg.GitHubToken, target, reportName); err != nil {
			return err
		}

		writeJSON(w, http.StatusAccepted, deployResponse{
			RequestID:   requestID,
			WorkflowURL: workflowURL(target),
			Target:      target,
			Skipped:     false,
			Message:     "workflow dispatch accepted",
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

		run, err := latestRunAfter(cfg.GitHubToken, state.Target, state.RequestedAt)
		if err != nil {
			return err
		}

		writeJSON(w, http.StatusOK, statusResponse{
			Status:     run.Status,
			Conclusion: run.Conclusion,
			RunURL:     run.HTMLURL,
			RunID:      run.ID,
			RunNumber:  run.RunNumber,
			RunAttempt: run.RunAttempt,
			CreatedAt:  run.CreatedAt.Format(time.RFC3339),
			UpdatedAt:  run.UpdatedAt.Format(time.RFC3339),
			Target:     state.Target,
			ReportName: state.ReportName,
		})
		return nil
	}))

	http.HandleFunc("/api/overview", withJSON(func(w http.ResponseWriter, r *http.Request) error {
		resp := overviewResponse{
			GeneratedAt: time.Now().Format(time.RFC3339),
			Checks:      runOverviewChecks(cfg),
		}
		writeJSON(w, http.StatusOK, resp)
		return nil
	}))

	log.Printf("portal listening on http://localhost:%s", cfg.Port)
	log.Fatal(http.ListenAndServe(":"+cfg.Port, nil))
}

func normalizedTarget(cfg config, in dispatchTarget) dispatchTarget {
	target := dispatchTarget{
		Owner:        strings.TrimSpace(in.Owner),
		Repo:         strings.TrimSpace(in.Repo),
		WorkflowFile: strings.TrimSpace(in.WorkflowFile),
		WorkflowRef:  strings.TrimSpace(in.WorkflowRef),
		Scope:        strings.TrimSpace(in.Scope),
		AppName:      strings.TrimSpace(in.AppName),
		ReleaseRing:  strings.TrimSpace(in.ReleaseRing),
		Namespace:    strings.TrimSpace(in.Namespace),
	}
	if target.Owner == "" {
		target.Owner = cfg.DefaultOwner
	}
	if target.Repo == "" {
		target.Repo = cfg.DefaultRepo
	}
	if target.WorkflowFile == "" {
		target.WorkflowFile = cfg.DefaultWorkflow
	}
	if target.WorkflowRef == "" {
		target.WorkflowRef = cfg.DefaultRef
	}
	if target.Scope == "" {
		target.Scope = cfg.DefaultScope
	}
	if target.AppName == "" {
		target.AppName = cfg.DefaultApp
	}
	if target.ReleaseRing == "" {
		target.ReleaseRing = cfg.DefaultRing
	}
	if target.Namespace == "" {
		target.Namespace = cfg.DefaultNamespace
	}
	return target
}

func triggerWorkflow(token string, target dispatchTarget, reportName string) error {
	body := workflowDispatchBody{
		Ref: target.WorkflowRef,
		Inputs: map[string]string{
			"scope":        target.Scope,
			"app_name":     target.AppName,
			"report_name":  reportName,
			"release_ring": target.ReleaseRing,
		},
	}
	payload, err := json.Marshal(body)
	if err != nil {
		return err
	}

	req, err := http.NewRequest(
		http.MethodPost,
		fmt.Sprintf("https://api.github.com/repos/%s/%s/actions/workflows/%s/dispatches", target.Owner, target.Repo, target.WorkflowFile),
		bytes.NewReader(payload),
	)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusNoContent {
		bodyBytes, _ := io.ReadAll(resp.Body)
		msg := strings.TrimSpace(string(bodyBytes))
		if msg == "" {
			msg = fmt.Sprintf("workflow dispatch was not accepted (github status %d)", resp.StatusCode)
		} else {
			msg = fmt.Sprintf("workflow dispatch was not accepted (github status %d): %s", resp.StatusCode, msg)
		}
		return apiError{Code: http.StatusBadGateway, Message: msg}
	}
	return nil
}

func latestRunAfter(token string, target dispatchTarget, requestedAt time.Time) (workflowRun, error) {
	req, err := http.NewRequest(
		http.MethodGet,
		fmt.Sprintf("https://api.github.com/repos/%s/%s/actions/workflows/%s/runs?branch=%s&event=workflow_dispatch&per_page=15", target.Owner, target.Repo, target.WorkflowFile, target.WorkflowRef),
		nil,
	)
	if err != nil {
		return workflowRun{}, err
	}
	req.Header.Set("Authorization", "Bearer "+token)
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
		if run.CreatedAt.After(requestedAt.Add(-10 * time.Second)) {
			return run, nil
		}
	}

	return workflowRun{
		Status:  "queued",
		HTMLURL: workflowURL(target),
	}, nil
}

func runOverviewChecks(cfg config) []checkItem {
	checks := []checkItem{
		checkTerraformWorkspace(),
		checkAWSIdentity(),
		checkGitHubRepo(cfg),
		checkKubernetesNodes(),
		checkArgoApplications(),
		checkNamespaceDeployments(cfg.DefaultNamespace),
		checkPrometheus(cfg.PrometheusURL),
		checkGrafana(cfg.GrafanaURL),
		checkLoki(),
	}
	return checks
}

func checkTerraformWorkspace() checkItem {
	path := filepath.Clean("../infrastructure/terraform/training")
	info, err := os.Stat(path)
	if err != nil || !info.IsDir() {
		return checkItem{Name: "Terraform Workspace", Status: "warn", Detail: "terraform workspace not found at ../infrastructure/terraform/training"}
	}
	return checkItem{Name: "Terraform Workspace", Status: "ok", Detail: "workspace found at ../infrastructure/terraform/training"}
}

func checkAWSIdentity() checkItem {
	out, err := runCommand(15*time.Second, "aws", "sts", "get-caller-identity", "--output", "json")
	if err != nil {
		return checkItem{Name: "AWS Identity", Status: "warn", Detail: err.Error(), Command: "aws sts get-caller-identity --output json"}
	}
	var parsed map[string]any
	if json.Unmarshal([]byte(out), &parsed) != nil {
		return checkItem{Name: "AWS Identity", Status: "warn", Detail: "received non-json response"}
	}
	arn, _ := parsed["Arn"].(string)
	account, _ := parsed["Account"].(string)
	return checkItem{Name: "AWS Identity", Status: "ok", Detail: fmt.Sprintf("account=%s arn=%s", account, arn)}
}

func checkGitHubRepo(cfg config) checkItem {
	if cfg.GitHubToken == "" {
		return checkItem{Name: "GitHub Repo Access", Status: "warn", Detail: "GITHUB_TOKEN is not set"}
	}
	url := fmt.Sprintf("https://api.github.com/repos/%s/%s", cfg.DefaultOwner, cfg.DefaultRepo)
	req, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		return checkItem{Name: "GitHub Repo Access", Status: "warn", Detail: err.Error()}
	}
	req.Header.Set("Authorization", "Bearer "+cfg.GitHubToken)
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return checkItem{Name: "GitHub Repo Access", Status: "warn", Detail: err.Error()}
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return checkItem{Name: "GitHub Repo Access", Status: "warn", Detail: fmt.Sprintf("status=%d %s", resp.StatusCode, strings.TrimSpace(string(body)))}
	}
	var repo githubRepoResponse
	if err := json.NewDecoder(resp.Body).Decode(&repo); err != nil {
		return checkItem{Name: "GitHub Repo Access", Status: "ok", Detail: "repo reachable"}
	}
	return checkItem{Name: "GitHub Repo Access", Status: "ok", Detail: fmt.Sprintf("repo=%s private=%v", repo.FullName, repo.Private)}
}

func checkKubernetesNodes() checkItem {
	out, err := runCommand(15*time.Second, "kubectl", "--request-timeout=10s", "get", "nodes", "--no-headers")
	if err != nil {
		return checkItem{Name: "Kubernetes Cluster", Status: "warn", Detail: err.Error(), Command: "kubectl get nodes --no-headers"}
	}
	lines := nonEmptyLines(out)
	return checkItem{Name: "Kubernetes Cluster", Status: "ok", Detail: fmt.Sprintf("connected, nodes=%d", len(lines))}
}

func checkArgoApplications() checkItem {
	out, err := runCommand(15*time.Second, "kubectl", "--request-timeout=10s", "-n", "argocd", "get", "applications", "--no-headers")
	if err != nil {
		return checkItem{Name: "Argo CD Applications", Status: "warn", Detail: err.Error(), Command: "kubectl -n argocd get applications --no-headers"}
	}
	lines := nonEmptyLines(out)
	return checkItem{Name: "Argo CD Applications", Status: "ok", Detail: fmt.Sprintf("applications=%d", len(lines))}
}

func checkNamespaceDeployments(namespace string) checkItem {
	out, err := runCommand(15*time.Second, "kubectl", "--request-timeout=10s", "-n", namespace, "get", "deploy", "--no-headers")
	if err != nil {
		return checkItem{Name: "K8s Deployments", Status: "warn", Detail: err.Error(), Command: fmt.Sprintf("kubectl -n %s get deploy --no-headers", namespace)}
	}
	lines := nonEmptyLines(out)
	return checkItem{Name: "K8s Deployments", Status: "ok", Detail: fmt.Sprintf("namespace=%s deployments=%d", namespace, len(lines))}
}

func checkPrometheus(baseURL string) checkItem {
	client := &http.Client{Timeout: 5 * time.Second}
	req, err := http.NewRequest(http.MethodGet, baseURL+"/api/v1/query?query=up", nil)
	if err != nil {
		return checkItem{Name: "Prometheus", Status: "warn", Detail: err.Error()}
	}
	resp, err := client.Do(req)
	if err != nil {
		return checkItem{Name: "Prometheus", Status: "warn", Detail: err.Error() + " (start port-forward: kubectl -n monitoring port-forward svc/kube-prometheus-stack-prometheus 9090:9090)"}
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return checkItem{Name: "Prometheus", Status: "warn", Detail: fmt.Sprintf("status=%d from %s", resp.StatusCode, baseURL)}
	}
	return checkItem{Name: "Prometheus", Status: "ok", Detail: "query endpoint reachable"}
}

func checkGrafana(baseURL string) checkItem {
	client := &http.Client{Timeout: 5 * time.Second}
	resp, err := client.Get(baseURL + "/api/health")
	if err != nil {
		return checkItem{Name: "Grafana", Status: "warn", Detail: err.Error() + " (start port-forward: kubectl -n monitoring port-forward svc/kube-prometheus-stack-grafana 3000:80)"}
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusUnauthorized {
		return checkItem{Name: "Grafana", Status: "ok", Detail: "reachable (auth required)"}
	}
	if resp.StatusCode != http.StatusOK {
		return checkItem{Name: "Grafana", Status: "warn", Detail: fmt.Sprintf("status=%d from %s", resp.StatusCode, baseURL)}
	}
	return checkItem{Name: "Grafana", Status: "ok", Detail: "health endpoint reachable"}
}

func checkLoki() checkItem {
	out, err := runCommand(15*time.Second, "kubectl", "--request-timeout=10s", "-n", "monitoring", "get", "pods", "-l", "app.kubernetes.io/name=loki", "--no-headers")
	if err != nil {
		return checkItem{Name: "Loki", Status: "warn", Detail: err.Error(), Command: "kubectl -n monitoring get pods -l app.kubernetes.io/name=loki --no-headers"}
	}
	lines := nonEmptyLines(out)
	if len(lines) == 0 {
		return checkItem{Name: "Loki", Status: "warn", Detail: "no loki pods found"}
	}
	return checkItem{Name: "Loki", Status: "ok", Detail: strings.TrimSpace(lines[0])}
}

func runCommand(timeout time.Duration, name string, args ...string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, name, args...)
	output, err := cmd.CombinedOutput()
	text := strings.TrimSpace(string(output))
	if ctx.Err() == context.DeadlineExceeded {
		return "", fmt.Errorf("command timeout: %s %s", name, strings.Join(args, " "))
	}
	if err != nil {
		if text == "" {
			return "", err
		}
		return "", fmt.Errorf("%s", text)
	}
	return text, nil
}

func nonEmptyLines(s string) []string {
	lines := strings.Split(s, "\n")
	out := make([]string, 0, len(lines))
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line != "" {
			out = append(out, line)
		}
	}
	return out
}

func workflowURL(target dispatchTarget) string {
	return fmt.Sprintf("https://github.com/%s/%s/actions/workflows/%s", target.Owner, target.Repo, target.WorkflowFile)
}

func hasMeaningfulChange(token string, target dispatchTarget, reportName string) (bool, string, error) {
	if token == "" {
		return true, "", nil
	}

	shared, err := githubFileContent(token, target, "deploy/apps/shared-values.yaml")
	if err != nil {
		return true, "", nil
	}
	sharedRing := extractQuotedValue(shared, "RELEASE_RING:")
	if sharedRing == "" {
		sharedRing = extractBareValue(shared, "RELEASE_RING:")
	}

	if target.Scope == "single-app" {
		appPath := fmt.Sprintf("deploy/apps/%s.values.yaml", target.AppName)
		appValues, err := githubFileContent(token, target, appPath)
		if err != nil {
			return true, "", nil
		}
		currentReport := extractQuotedValue(appValues, "REPORT_NAME:")
		currentAppRing := extractQuotedValue(appValues, "RELEASE_RING:")
		if currentAppRing == "" {
			currentAppRing = sharedRing
		}
		if currentReport == reportName && currentAppRing == target.ReleaseRing {
			return false, "no changes detected: report_name and release_ring are already set for this app", nil
		}
		return true, "", nil
	}

	// all-apps path: report_name is not applied across all app value files in current workflow logic.
	if sharedRing == target.ReleaseRing {
		return false, "no changes detected: shared release_ring already matches requested value", nil
	}
	return true, "", nil
}

func githubFileContent(token string, target dispatchTarget, path string) (string, error) {
	url := fmt.Sprintf("https://api.github.com/repos/%s/%s/contents/%s?ref=%s", target.Owner, target.Repo, path, target.WorkflowRef)
	req, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return "", fmt.Errorf("could not read %s: status=%d %s", path, resp.StatusCode, strings.TrimSpace(string(body)))
	}

	var payload struct {
		Content  string `json:"content"`
		Encoding string `json:"encoding"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		return "", err
	}
	raw := strings.ReplaceAll(payload.Content, "\n", "")
	if payload.Encoding != "base64" {
		return "", fmt.Errorf("unsupported github content encoding for %s: %s", path, payload.Encoding)
	}
	decoded, err := base64.StdEncoding.DecodeString(raw)
	if err != nil {
		return "", err
	}
	return string(decoded), nil
}

func extractQuotedValue(content string, key string) string {
	for _, line := range strings.Split(content, "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, key) {
			rest := strings.TrimSpace(strings.TrimPrefix(line, key))
			rest = strings.Trim(rest, "\"")
			return rest
		}
	}
	return ""
}

func extractBareValue(content string, key string) string {
	for _, line := range strings.Split(content, "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, key) {
			return strings.TrimSpace(strings.TrimPrefix(line, key))
		}
	}
	return ""
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
    <title>Platform Control Center</title>
    <style>
      :root {
        --bg: #0a0f19;
        --panel: #111a2b;
        --panel-2: #142136;
        --line: #253a5d;
        --text: #e5edf9;
        --muted: #9fb2d1;
        --ok: #30b37b;
        --warn: #f0b34a;
        --err: #e25d6d;
        --brand: #4f89ff;
      }
      * { box-sizing: border-box; }
      body {
        margin: 0;
        background: radial-gradient(circle at 20% -10%, #18335f 0, transparent 35%), var(--bg);
        color: var(--text);
        font-family: Inter, ui-sans-serif, system-ui, -apple-system, Segoe UI, Roboto, Arial, sans-serif;
      }
      .shell { max-width: 1220px; margin: 0 auto; padding: 24px; }
      .hero { display: grid; gap: 8px; margin-bottom: 18px; }
      .hero h1 { margin: 0; font-size: 32px; font-weight: 700; }
      .hero p { margin: 0; color: var(--muted); }
      .grid {
        display: grid;
        gap: 14px;
        grid-template-columns: 1.1fr 0.9fr;
      }
      .panel {
        background: linear-gradient(180deg, var(--panel), var(--panel-2));
        border: 1px solid var(--line);
        border-radius: 8px;
        padding: 16px;
      }
      .title { margin: 0 0 10px; font-size: 14px; text-transform: uppercase; color: var(--muted); letter-spacing: 0.04em; }
      .form-grid {
        display: grid;
        grid-template-columns: 1fr 1fr;
        gap: 10px;
      }
      .row-full { grid-column: 1 / -1; }
      label { display: block; font-size: 12px; color: var(--muted); margin-bottom: 6px; }
      input, select {
        width: 100%;
        padding: 10px 12px;
        border: 1px solid #2c4268;
        border-radius: 6px;
        background: #0f1727;
        color: var(--text);
      }
      .btns { display: flex; gap: 10px; margin-top: 12px; }
      button {
        border: 1px solid #3a5381;
        background: #17315b;
        color: #e8f0ff;
        padding: 10px 14px;
        border-radius: 6px;
        cursor: pointer;
      }
      button.primary { background: var(--brand); border-color: var(--brand); color: #08152d; font-weight: 600; }
      .mono { font-family: ui-monospace, SFMono-Regular, Menlo, Consolas, monospace; font-size: 12px; }
      .status-box {
        min-height: 160px;
        white-space: pre-wrap;
        background: #0b1220;
        border: 1px solid #273e63;
        border-radius: 6px;
        padding: 12px;
        font-size: 13px;
        line-height: 1.45;
      }
      .checks {
        margin-top: 14px;
        display: grid;
        grid-template-columns: repeat(3, minmax(0, 1fr));
        gap: 10px;
      }
      .check {
        border: 1px solid #2a3f64;
        border-radius: 6px;
        padding: 10px;
        background: #0f192c;
      }
      .check .name { font-size: 12px; color: var(--muted); margin-bottom: 4px; }
      .badge {
        display: inline-block;
        padding: 2px 8px;
        border-radius: 999px;
        font-size: 11px;
        font-weight: 600;
      }
      .ok { background: rgba(48,179,123,0.15); color: var(--ok); border: 1px solid rgba(48,179,123,0.45); }
      .warn { background: rgba(240,179,74,0.12); color: var(--warn); border: 1px solid rgba(240,179,74,0.35); }
      .err { background: rgba(226,93,109,0.12); color: var(--err); border: 1px solid rgba(226,93,109,0.35); }
      .detail { margin-top: 6px; color: #d0dcf3; font-size: 12px; white-space: pre-wrap; word-break: break-word; }
      @media (max-width: 960px) {
        .grid { grid-template-columns: 1fr; }
        .checks { grid-template-columns: 1fr; }
      }
    </style>
  </head>
  <body>
    <div class="shell">
      <div class="hero">
        <h1>Platform Control Center</h1>
        <p>Trigger GitOps deployments and prove platform readiness across AWS, Kubernetes, Argo CD, Prometheus, Grafana, and Loki.</p>
      </div>

      <div class="grid">
        <section class="panel">
          <h2 class="title">Deployment Request</h2>
          <form id="deploy-form" class="form-grid">
            <div><label>GitHub Owner</label><input id="owner" value="{{ .DefaultOwner }}" /></div>
            <div><label>GitHub Repo</label><input id="repo" value="{{ .DefaultRepo }}" /></div>
            <div><label>Workflow File</label><input id="workflowFile" value="{{ .DefaultWorkflow }}" /></div>
            <div><label>Workflow Ref</label><input id="workflowRef" value="{{ .DefaultRef }}" /></div>
            <div>
              <label>Scope</label>
              <select id="scope">
                <option value="single-app" selected>single-app</option>
                <option value="all-apps">all-apps</option>
              </select>
            </div>
            <div><label>App Name</label><input id="appName" value="{{ .DefaultApp }}" /></div>
            <div><label>Release Ring</label><input id="releaseRing" value="{{ .DefaultRing }}" /></div>
            <div><label>Namespace</label><input id="namespace" value="{{ .DefaultNamespace }}" /></div>
            <div class="row-full"><label>Report Name</label><input id="reportName" value="platform-demo" /></div>
            <div class="row-full btns">
              <button class="primary" type="submit">Dispatch Workflow</button>
              <button id="refreshChecks" type="button">Refresh Platform Checks</button>
            </div>
          </form>
        </section>

        <section class="panel">
          <h2 class="title">Workflow and Rollout Status</h2>
          <div id="status" class="status-box mono">No active request yet. Dispatch a workflow to start live tracking.</div>
        </section>
      </div>

      <section class="panel" style="margin-top:14px;">
        <h2 class="title">Platform Health Proof</h2>
        <div id="overviewMeta" class="mono" style="margin-bottom:8px;color:#b6c7e3;">Loading checks...</div>
        <div id="checks" class="checks"></div>
      </section>
    </div>

    <script>
      const form = document.getElementById("deploy-form");
      const statusNode = document.getElementById("status");
      const checksNode = document.getElementById("checks");
      const overviewMetaNode = document.getElementById("overviewMeta");
      const refreshChecksBtn = document.getElementById("refreshChecks");
      let timer = null;

      function statusClass(v) {
        if (v === "ok") return "ok";
        if (v === "warn") return "warn";
        return "err";
      }

      function renderChecks(items) {
        checksNode.innerHTML = "";
        items.forEach((item) => {
          const div = document.createElement("div");
          div.className = "check";
          div.innerHTML =
            "<div class=\"name\">" + item.name + "</div>" +
            "<span class=\"badge " + statusClass(item.status) + "\">" + item.status.toUpperCase() + "</span>" +
            "<div class=\"detail\">" + (item.detail || "") + "</div>";
          checksNode.appendChild(div);
        });
      }

      async function loadOverview() {
        overviewMetaNode.textContent = "Loading checks...";
        try {
          const response = await fetch("/api/overview");
          const data = await response.json();
          if (!response.ok) {
            overviewMetaNode.textContent = data.error || "Failed to load checks";
            return;
          }
          overviewMetaNode.textContent = "Last refresh: " + data.generatedAt;
          renderChecks(data.checks || []);
        } catch (err) {
          overviewMetaNode.textContent = "Failed to load checks: " + err;
        }
      }

      function targetFromForm() {
        return {
          owner: document.getElementById("owner").value.trim(),
          repo: document.getElementById("repo").value.trim(),
          workflowFile: document.getElementById("workflowFile").value.trim(),
          workflowRef: document.getElementById("workflowRef").value.trim(),
          scope: document.getElementById("scope").value.trim(),
          appName: document.getElementById("appName").value.trim(),
          releaseRing: document.getElementById("releaseRing").value.trim(),
          namespace: document.getElementById("namespace").value.trim()
        };
      }

      async function pollStatus(requestId) {
        const response = await fetch("/api/status?requestId=" + encodeURIComponent(requestId));
        const data = await response.json();
        if (!response.ok) {
          statusNode.textContent = data.error || "Failed to fetch status";
          return;
        }

        statusNode.textContent =
          "Status: " + data.status + "\n" +
          "Conclusion: " + (data.conclusion || "pending") + "\n" +
          "Run URL: " + (data.runUrl || "waiting") + "\n" +
          "Run ID: " + (data.runId || 0) + "\n" +
          "Run Number / Attempt: " + (data.runNumber || 0) + " / " + (data.runAttempt || 0) + "\n" +
          "Created: " + (data.createdAt || "-") + "\n" +
          "Updated: " + (data.updatedAt || "-") + "\n" +
          "Target: " + data.target.owner + "/" + data.target.repo + " -> " + data.target.workflowFile + "\n" +
          "Scope/App/Ring: " + data.target.scope + " / " + data.target.appName + " / " + data.target.releaseRing + "\n" +
          "Report: " + data.reportName;

        if (data.status !== "completed") {
          timer = setTimeout(() => pollStatus(requestId), 5000);
        } else {
          loadOverview();
        }
      }

      form.addEventListener("submit", async (event) => {
        event.preventDefault();
        clearTimeout(timer);
        statusNode.textContent = "Dispatching workflow...";
        try {
          const body = {
            reportName: document.getElementById("reportName").value.trim(),
            target: targetFromForm()
          };
          const response = await fetch("/api/deploy", {
            method: "POST",
            headers: { "Content-Type": "application/json" },
            body: JSON.stringify(body)
          });
          const data = await response.json();
          if (!response.ok) {
            statusNode.textContent = data.error || "Dispatch failed";
            return;
          }
          if (data.skipped) {
            statusNode.textContent =
              "Dispatch skipped.\n" +
              (data.message || "No meaningful changes detected.") + "\n" +
              "Workflow Page: " + (data.workflowUrl || "-");
            return;
          }
          statusNode.textContent =
            "Workflow dispatch accepted.\n" +
            "Request ID: " + data.requestId + "\n" +
            "Workflow Page: " + data.workflowUrl + "\n" +
            "Polling live status...";
          pollStatus(data.requestId);
        } catch (err) {
          statusNode.textContent = "Dispatch failed: " + err;
        }
      });

      refreshChecksBtn.addEventListener("click", () => loadOverview());
      loadOverview();
    </script>
  </body>
</html>`
