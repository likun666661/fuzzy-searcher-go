package config

import (
	"fmt"
	"os"
	"strings"
)

const ValidationSchemaVersion = "service-config-check/v1"

// ValidationReport is the stable startup/profile configuration check envelope.
type ValidationReport struct {
	SchemaVersion string            `json:"schema_version"`
	Profile       string            `json:"profile"`
	Ready         bool              `json:"ready"`
	Checks        []ValidationCheck `json:"checks"`
}

// ValidationCheck records one startup configuration check.
type ValidationCheck struct {
	Name     string `json:"name"`
	Status   string `json:"status"`
	Required bool   `json:"required"`
	Message  string `json:"message,omitempty"`
	Path     string `json:"path,omitempty"`
	Value    string `json:"value,omitempty"`
}

// Validate returns a machine-readable startup configuration report.
func Validate(cfg Config) ValidationReport {
	report := ValidationReport{
		SchemaVersion: ValidationSchemaVersion,
		Profile:       profile(cfg),
		Ready:         true,
	}
	add := func(check ValidationCheck) {
		if check.Status == "" {
			check.Status = "ok"
		}
		if check.Required && check.Status == "failed" {
			report.Ready = false
		}
		report.Checks = append(report.Checks, check)
	}

	allowedProfile := isAllowedProfile(report.Profile)
	add(ValidationCheck{
		Name:     "profile",
		Required: true,
		Status:   status(allowedProfile),
		Value:    report.Profile,
		Message:  profileMessage(report.Profile, allowedProfile),
	})
	add(ValidationCheck{
		Name:     "http_addr",
		Required: true,
		Status:   status(strings.TrimSpace(cfg.HTTPAddr) != ""),
		Value:    cfg.HTTPAddr,
		Message:  requiredMessage(strings.TrimSpace(cfg.HTTPAddr) != "", "YOUTU_RAG_HTTP_ADDR is required"),
	})
	add(ValidationCheck{
		Name:     "default_dataset",
		Required: true,
		Status:   status(strings.TrimSpace(cfg.DefaultDataset) != ""),
		Value:    cfg.DefaultDataset,
		Message:  requiredMessage(strings.TrimSpace(cfg.DefaultDataset) != "", "YOUTU_RAG_DATASET is required"),
	})
	add(pathCheck("artifact_root", cfg.ArtifactRoot, requiresArtifacts(report.Profile)))
	add(pathCheck("worker_cwd", cfg.WorkerCWD, requiresWorkers(report.Profile)))
	add(pathCheck("python_bin", cfg.PythonBin, requiresWorkers(report.Profile)))
	add(pathCheck("golden_script", cfg.GoldenScript, requiresWorkers(report.Profile)))
	add(pathCheck("parse_documents_script", cfg.ParseDocsScript, requiresWorkers(report.Profile)))
	add(pathCheck("build_graph_script", cfg.BuildGraphScript, requiresWorkers(report.Profile)))
	add(pathCheck("answer_script", cfg.AnswerScript, requiresWorkers(report.Profile)))
	add(pathCheck("default_graph", cfg.DefaultGraph, requiresDemoRetrieval(report.Profile)))
	add(pathCheck("default_chunks", cfg.DefaultChunks, requiresDemoRetrieval(report.Profile)))
	add(ValidationCheck{
		Name:     "sidecar_url",
		Required: requiresSidecar(report.Profile, cfg.DefaultMode),
		Status:   status(!requiresSidecar(report.Profile, cfg.DefaultMode) || strings.TrimSpace(cfg.DefaultSidecar) != ""),
		Value:    cfg.DefaultSidecar,
		Message:  sidecarMessage(report.Profile, cfg.DefaultMode, cfg.DefaultSidecar),
	})
	return report
}

// Err returns a concise error when the report is not ready.
func (r ValidationReport) Err() error {
	if r.Ready {
		return nil
	}
	failed := make([]string, 0)
	for _, check := range r.Checks {
		if check.Required && check.Status == "failed" {
			if check.Message != "" {
				failed = append(failed, fmt.Sprintf("%s: %s", check.Name, check.Message))
			} else {
				failed = append(failed, check.Name)
			}
		}
	}
	return fmt.Errorf("service configuration is not ready: %s", strings.Join(failed, "; "))
}

func pathCheck(name string, path string, required bool) ValidationCheck {
	check := ValidationCheck{
		Name:     name,
		Required: required,
		Path:     path,
	}
	if path == "" {
		if required {
			check.Status = "failed"
			check.Message = envName(name) + " is required"
		} else {
			check.Status = "skipped"
			check.Message = "not configured"
		}
		return check
	}
	if _, err := os.Stat(path); err != nil {
		if required {
			check.Status = "failed"
		} else {
			check.Status = "warning"
		}
		check.Message = err.Error()
		return check
	}
	check.Status = "ok"
	return check
}

func profile(cfg Config) string {
	value := strings.TrimSpace(cfg.Profile)
	if value == "" {
		return "local"
	}
	return value
}

func isAllowedProfile(profile string) bool {
	switch profile {
	case "local", "demo", "production":
		return true
	default:
		return false
	}
}

func requiresArtifacts(profile string) bool {
	return profile == "demo" || profile == "production"
}

func requiresDemoRetrieval(profile string) bool {
	return profile == "demo"
}

func requiresWorkers(profile string) bool {
	return profile == "production"
}

func requiresSidecar(profile string, mode string) bool {
	if profile == "demo" {
		return mode != "native"
	}
	return profile == "production"
}

func status(ok bool) string {
	if ok {
		return "ok"
	}
	return "failed"
}

func requiredMessage(ok bool, message string) string {
	if ok {
		return ""
	}
	return message
}

func profileMessage(profile string, ok bool) string {
	if ok {
		return ""
	}
	return fmt.Sprintf("unsupported YOUTU_RAG_PROFILE %q; expected local, demo, or production", profile)
}

func sidecarMessage(profile string, mode string, sidecarURL string) string {
	if !requiresSidecar(profile, mode) || strings.TrimSpace(sidecarURL) != "" {
		return ""
	}
	return "YOUTU_RAG_SIDECAR_URL is required for this profile/mode"
}

func envName(name string) string {
	switch name {
	case "artifact_root":
		return "YOUTU_RAG_ARTIFACT_ROOT"
	case "worker_cwd":
		return "YOUTU_RAG_WORKER_CWD"
	case "python_bin":
		return "YOUTU_RAG_PYTHON"
	case "golden_script":
		return "YOUTU_RAG_GOLDEN_SCRIPT"
	case "parse_documents_script":
		return "YOUTU_RAG_PARSE_DOCUMENTS_SCRIPT"
	case "build_graph_script":
		return "YOUTU_RAG_BUILD_GRAPH_SCRIPT"
	case "answer_script":
		return "YOUTU_RAG_ANSWER_SCRIPT"
	case "default_graph":
		return "YOUTU_RAG_GRAPH"
	case "default_chunks":
		return "YOUTU_RAG_CHUNKS"
	default:
		return name
	}
}
