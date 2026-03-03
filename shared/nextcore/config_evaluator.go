package nextcore

import (
	"encoding/json"
	"fmt"
	"os/exec"
	"strings"
)

// evaluateConfigViaRuntime uses Node or Bun to safely load and evaluate next.config.js/mjs
func evaluateConfigViaRuntime(configPath string) (map[string]interface{}, error) {
	// Simple JS script to import the config and print it as JSON
	// We handle default exports, promises, and functions that return config objects.
	scriptContent := `
async function load() {
    try {
        let mod = await import('file://'+process.argv[1]);
        let cfg = mod.default || mod;
        
        if (typeof cfg === 'function') {
            cfg = await cfg('phase-production-build', { defaultConfig: {} });
        } else if (cfg instanceof Promise) {
            cfg = await cfg;
        }

        // Output JSON, stripping functions/regex which JSON.stringify does naturally
        console.log(JSON.stringify(cfg, null, 2));
    } catch(e) {
        console.error("Config Eval Error: ", e.message);
        process.exit(1);
    }
}
load();
`

	var runtime string
	var args []string

	// Check for bun first since the user uses it
	bunPath, err := exec.LookPath("bun")
	if err == nil {
		runtime = bunPath
		args = []string{"-e", scriptContent, configPath}
	} else if nodePath, err := exec.LookPath("node"); err == nil {
		runtime = nodePath
		args = []string{"-e", scriptContent, configPath}
	} else {
		return nil, fmt.Errorf("neither node nor bun found in PATH to evaluate config")
	}

	cmd := exec.Command(runtime, args...)
	// #nosec G204
	out, err := cmd.Output()
	if err != nil {
		var stderrStr string
		if ee, ok := err.(*exec.ExitError); ok {
			stderrStr = string(ee.Stderr)
		}
		return nil, fmt.Errorf("failed to evaluate config via %s: %w\nOutput: %s\nStderr: %s", runtime, err, string(out), stderrStr)
	}

	var parsed map[string]interface{}
	// Use UseNumber to avoid float64 precision issues for ints
	decoder := json.NewDecoder(strings.NewReader(string(out)))
	decoder.UseNumber()

	if err := decoder.Decode(&parsed); err != nil {
		return nil, fmt.Errorf("failed to parse JSON from config evaluator: %w", err)
	}

	return parsed, nil
}
