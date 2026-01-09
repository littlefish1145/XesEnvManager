package executor

import (
	"python-manager/pkg/config"
	"testing"
)

func TestWSLPythonExecutor_Configuration(t *testing.T) {
	cfg := &config.PythonConfig{}
	executor := NewWSLPythonExecutor(cfg)

	// Test default values
	if !executor.interactive {
		t.Error("NewWSLPythonExecutor should have interactive=true by default")
	}
	if !executor.captureOutput {
		t.Error("NewWSLPythonExecutor should have captureOutput=true by default")
	}
	if !executor.autoExit {
		t.Error("NewWSLPythonExecutor should have autoExit=true by default")
	}

	// Test configuration methods
	executor.SetInteractive(false)
	if executor.interactive {
		t.Error("SetInteractive(false) failed")
	}

	executor.SetCaptureOutput(false)
	if executor.captureOutput {
		t.Error("SetCaptureOutput(false) failed")
	}

	executor.SetAutoExit(false)
	if executor.autoExit {
		t.Error("SetAutoExit(false) failed")
	}

	args := []string{"arg1", "arg2"}
	executor.SetArguments(args)
	if len(executor.args) != 2 || executor.args[0] != "arg1" || executor.args[1] != "arg2" {
		t.Error("SetArguments failed")
	}
}
