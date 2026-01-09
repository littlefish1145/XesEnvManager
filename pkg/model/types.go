package model

// PythonEnvironment represents a Python environment
type PythonEnvironment struct {
	Name     string `json:"name"`
	Path     string `json:"path"`
	Version  string `json:"version"`
	Enabled  bool   `json:"enabled"`
	Encoding string `json:"encoding"`
	Platform string `json:"platform"` // wsl or windows
}
