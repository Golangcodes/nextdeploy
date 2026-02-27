# Complete Gosec Security Issues Analysis for NextDeploy

## 📋 Summary
- **Total Issues**: 180
- **Files Scanned**: 92
- **Lines of Code**: 13,634

---

## 🔴 **HIGH SEVERITY ISSUES** (21 issues)

### 1. G115 - Integer Overflow Conversion (1 issue)
**File**: `daemon/internal/daemon/command_handler.go:311`
```go
outFile, err := os.OpenFile(target, os.O_CREATE|os.O_RDWR, os.FileMode(header.Mode))
```
**Problem**: Converting `int64` (header.Mode) to `uint32` without bounds checking
**Solution**:
```go
if header.Mode < 0 || header.Mode > 0777 {
    return fmt.Errorf("invalid file mode: %v", header.Mode)
}
outFile, err := os.OpenFile(target, os.O_CREATE|os.O_RDWR, os.FileMode(header.Mode))
```

### 2. G704 - SSRF (Server-Side Request Forgery) (6 issues)
**Files**:
- `shared/registry/digitalocean.go:86`
- `shared/caddy/caddy.go:43,116,142,163,194`

**Problem**: Making HTTP requests to URLs that could be manipulated
**Solution**:
```go
// Add URL validation
func isValidURL(url string) bool {
    parsed, err := url.Parse(url)
    if err != nil {
        return false
    }
    allowedHosts := []string{"api.digitalocean.com", "localhost", "127.0.0.1"}
    for _, host := range allowedHosts {
        if parsed.Host == host {
            return true
        }
    }
    return false
}

// Before making request
if !isValidURL(req.URL.String()) {
    return nil, fmt.Errorf("invalid URL: %s", req.URL)
}
resp, err := client.Do(req)
```

### 3. G703 - Path Traversal via Taint Analysis (10 issues)

| # | File | Line | Problem |
|---|------|------|---------|
| 1 | `shared/config/nextdeploy.generator.go` | 277 | Writing file with user-controlled filename |
| 2 | `daemon/internal/registry/registry.go` | 34 | Opening docker config with HOME env var |
| 3-5 | `daemon/internal/daemon/command_handler.go` | 306,310,311 | Multiple file operations with user input |
| 6-8 | `cli/cmd/build.go` | 368,525,540 | File operations with user input |
| 9-10 | `daemon/cmd/daemon/main.go` | 74 | Command execution |

**Solution**:
```go
// Validate all file paths
func safeJoin(base, path string) (string, error) {
    if strings.Contains(path, "..") {
        return "", fmt.Errorf("path traversal detected: %s", path)
    }
    cleanPath := filepath.Clean(path)
    fullPath := filepath.Join(base, cleanPath)
    if !strings.HasPrefix(fullPath, base) {
        return "", fmt.Errorf("path escapes base directory: %s", path)
    }
    return fullPath, nil
}

// For Go 1.24+, use os.DirFS
root := os.DirFS("/safe/base/directory")
data, err := fs.ReadFile(root, userProvidedPath)
```

### 4. G702 - Command Injection via Taint Analysis (3 issues)

| # | File | Line | Problem |
|---|------|------|---------|
| 1 | `daemon/cmd/daemon/main.go` | 74 | exec.Command with variable args |
| 2-3 | `daemon/cmd/client/client.go` | 112,131 | exec.Command with config path concatenation |

**Solution**:
```go
// Before
cmd := exec.Command("nextdeploy-daemon", "--config="+configPath)

// After - validate input
func validateConfigPath(path string) error {
    if path == "" {
        return fmt.Errorf("empty config path")
    }
    if strings.Contains(path, "..") {
        return fmt.Errorf("path traversal detected")
    }
    if !strings.HasPrefix(path, "/etc/nextdeploy/") && 
       !strings.HasPrefix(path, "./") {
        return fmt.Errorf("config path must be in /etc/nextdeploy/ or current directory")
    }
    return nil
}

if err := validateConfigPath(configPath); err != nil {
    return err
}
cmd := exec.Command("nextdeploy-daemon", "--config="+configPath)
```

---

## 🟡 **MEDIUM SEVERITY ISSUES** (119 issues)

### 5. G204 - Subprocess Launched with Variable (23 issues)

| Category | Files | Count |
|----------|-------|-------|
| Docker commands | `digitalocean.go`, `awsecr.go`, `registry.go` | 5 |
| Systemctl commands | `process_manager.go` | 5 |
| AWS CLI | `awsprofiles.go`, `awsecr.go` | 3 |
| Ansible | `prepare.go` | 3 |
| Other | `metadatafuncs.go`, `update.go`, `server.go`, `main.go` | 7 |

**Solution**:
```go
// Create a safe command executor
type SafeExecutor struct {
    allowedCommands map[string]bool
}

func (e *SafeExecutor) Exec(command string, args ...string) error {
    if !e.allowedCommands[command] {
        return fmt.Errorf("command not allowed: %s", command)
    }
    
    // Validate each argument
    for _, arg := range args {
        if err := validateArg(arg); err != nil {
            return err
        }
    }
    
    cmd := exec.Command(command, args...)
    return cmd.Run()
}

func validateArg(arg string) error {
    // Prevent command injection
    forbidden := []string{";", "&", "|", "$", "`", "\\", "&&", "||"}
    for _, f := range forbidden {
        if strings.Contains(arg, f) {
            return fmt.Errorf("forbidden character in argument: %s", f)
        }
    }
    return nil
}
```

### 6. G304 - Potential File Inclusion via Variable (41 issues)

**Files with most issues**:
- `shared/nextcore/` - 12 issues
- `cli/internal/server/` - 7 issues
- `daemon/internal/daemon/` - 5 issues
- `shared/secrets/` - 3 issues

**Solution - Path Validation**:
```go
type SafeFileSystem struct {
    root string
}

func NewSafeFileSystem(root string) *SafeFileSystem {
    return &SafeFileSystem{root: root}
}

func (fs *SafeFileSystem) Open(filename string) (*os.File, error) {
    // Clean and validate path
    cleanPath := filepath.Clean(filename)
    if strings.HasPrefix(cleanPath, "..") || strings.Contains(cleanPath, "../") {
        return nil, fmt.Errorf("path traversal detected")
    }
    
    fullPath := filepath.Join(fs.root, cleanPath)
    if !strings.HasPrefix(fullPath, fs.root) {
        return nil, fmt.Errorf("path escapes root")
    }
    
    return os.Open(fullPath)
}

func (fs *SafeFileSystem) ReadFile(filename string) ([]byte, error) {
    file, err := fs.Open(filename)
    if err != nil {
        return nil, err
    }
    defer file.Close()
    return io.ReadAll(file)
}
```

### 7. G107 - HTTP Request with Variable URL (3 issues)

| File | Line | Problem |
|------|------|---------|
| `shared/health/checker.go` | 21 | http.Get(url) in health check |
| `daemon/cmd/daemon/update.go` | 89,105 | http.Get(url) for GitHub API and downloads |

**Solution**:
```go
type SafeHTTPClient struct {
    client      *http.Client
    allowedHosts map[string]bool
}

func NewSafeHTTPClient() *SafeHTTPClient {
    return &SafeHTTPClient{
        client: &http.Client{
            Timeout: 10 * time.Second,
        },
        allowedHosts: map[string]bool{
            "api.github.com": true,
            "github.com":     true,
            "localhost":      true,
            "127.0.0.1":      true,
        },
    }
}

func (c *SafeHTTPClient) Get(url string) (*http.Response, error) {
    parsed, err := http.ParseURL(url)
    if err != nil {
        return nil, err
    }
    
    if !c.allowedHosts[parsed.Host] {
        return nil, fmt.Errorf("host not allowed: %s", parsed.Host)
    }
    
    // Prevent redirects to malicious sites
    c.client.CheckRedirect = func(req *http.Request, via []*http.Request) error {
        if len(via) >= 10 {
            return fmt.Errorf("too many redirects")
        }
        if !c.allowedHosts[req.URL.Host] {
            return fmt.Errorf("redirect to forbidden host: %s", req.URL.Host)
        }
        return nil
    }
    
    return c.client.Get(url)
}
```

### 8. G110 - Decompression Bomb (1 issue)
**File**: `daemon/internal/daemon/command_handler.go:315`
```go
if _, err := io.Copy(outFile, tarReader); err != nil {
```

**Solution**:
```go
const MaxDecompressedSize = 1024 * 1024 * 1024 // 1GB

// Use LimitedReader
limitedReader := io.LimitedReader{
    R: tarReader,
    N: MaxDecompressedSize,
}

if _, err := io.Copy(outFile, &limitedReader); err != nil {
    outFile.Close()
    return err
}

if limitedReader.N <= 0 {
    return fmt.Errorf("decompressed size exceeds limit of %d bytes", MaxDecompressedSize)
}
```

### 9. G305 - File Traversal in Archive (1 issue)
**File**: `daemon/internal/daemon/command_handler.go:297`
```go
target := filepath.Join(dest, header.Name)
```

**Solution**:
```go
func safeTarPath(dest, headerName string) (string, error) {
    // Clean the path and prevent traversal
    cleanPath := filepath.Clean(headerName)
    if strings.HasPrefix(cleanPath, "..") || strings.Contains(cleanPath, "../") {
        return "", fmt.Errorf("path traversal detected: %s", headerName)
    }
    
    target := filepath.Join(dest, cleanPath)
    
    // Ensure target is within dest
    if !strings.HasPrefix(target, filepath.Clean(dest)+string(os.PathSeparator)) {
        return "", fmt.Errorf("tar entry escapes destination: %s", headerName)
    }
    
    return target, nil
}

// Usage
target, err := safeTarPath(dest, header.Name)
if err != nil {
    return err
}
```

### 10. G117 - Secrets in Structs (11 issues)

**Files with secrets**:

| File | Field | Line |
|------|-------|------|
| `shared/config/types.go` | Secret, Password, AccessKey | 82,113,167,215,237,260 |
| `shared/nextdeploy/types.go` | Password, AccessKey | 83,123 |
| `shared/registry/awsecr.go` | AccessKey, SecretKey | 27-28 |

**Solution**:
```go
// Option 1: Use secret wrapper type
type Secret string

func (s Secret) String() string {
    return "***REDACTED***"
}

func (s Secret) GoString() string {
    return "***REDACTED***"
}

// Option 2: Implement custom marshaling
func (s Secret) MarshalJSON() ([]byte, error) {
    return []byte(`"***REDACTED***"`), nil
}

func (s *Secret) UnmarshalJSON(data []byte) error {
    // Actually unmarshal the real value
    var str string
    if err := json.Unmarshal(data, &str); err != nil {
        return err
    }
    *s = Secret(str)
    return nil
}

// Option 3: Mark fields as sensitive for logging
type Config struct {
    Username string `yaml:"username"`
    Password string `yaml:"password" sensitive:"true"`
    APIKey   string `yaml:"api_key" sensitive:"true"`
}

func (c Config) String() string {
    // Custom stringer that redacts sensitive fields
    return fmt.Sprintf("Config{Username: %s, Password: ***, APIKey: ***}", c.Username)
}
```

### 11. G301/G302/G306 - File/Directory Permissions (23 issues)

**Common patterns**:
- `os.MkdirAll(..., 0755)` - Should be 0750 for sensitive dirs
- `os.WriteFile(..., 0644)` - Should be 0600 for sensitive files
- `os.Chmod(..., 0755)` - Too permissive

**Solution - Permission Constants**:
```go
const (
    // Directory permissions
    DirPublic      = 0755 // world-readable, world-executable
    DirPrivate     = 0750 // owner+group only
    DirSecret      = 0700 // owner only
    
    // File permissions
    FilePublic     = 0644 // world-readable
    FilePrivate    = 0640 // owner+group readable
    FileSecret     = 0600 // owner only
    FileExecutable = 0755 // executable
)

// Safe file writer
func writeSensitiveFile(path string, data []byte) error {
    dir := filepath.Dir(path)
    if err := os.MkdirAll(dir, DirSecret); err != nil {
        return err
    }
    return os.WriteFile(path, data, FileSecret)
}

// Safe config writer
func writeConfigFile(path string, data []byte) error {
    dir := filepath.Dir(path)
    if err := os.MkdirAll(dir, DirPrivate); err != nil {
        return err
    }
    return os.WriteFile(path, data, FilePrivate)
}
```

---

## 🟢 **LOW SEVERITY ISSUES** (40 issues)

### 12. G103 - Use of Unsafe Calls (2 issues)
**File**: `shared/crypto.go:119,139`
```go
syscall.Syscall(syscall.SYS_MLOCK, uintptr(unsafe.Pointer(&key[0])), ...)
```

**Solution**:
```go
// Option 1: Use memguard (external library)
import "github.com/awnumar/memguard"

func lockMemory(key []byte) {
    lockedBuffer := memguard.NewBufferFromBytes(key)
    defer lockedBuffer.Destroy()
}

// Option 2: Use crypto/safe (Go 1.24+)
import "crypto/safe"

func lockMemory(key []byte) error {
    return safe.LockMemory(key)
}

// Option 3: If you must keep unsafe, document why
// WARNING: unsafe required for mlock syscall to prevent key from being swapped to disk
// This is acceptable because the key is only in memory temporarily
```

### 13. G104 - Errors Unhandled (55 issues) ⚠️ **MOST NUMEROUS**

**Common patterns**:
- `file.Close()` without error check
- `os.Remove()` without error check
- `io.Copy()` without error check
- `encoder.Encode()` without error check

**Solution - Error Handling Patterns**:

```go
// Pattern 1: Defer with error check
func processFile(path string) error {
    f, err := os.Open(path)
    if err != nil {
        return err
    }
    defer func() {
        if cerr := f.Close(); cerr != nil {
            // Log but don't overwrite original error
            log.Printf("error closing file: %v", cerr)
        }
    }()
    return nil
}

// Pattern 2: Multi-error handling
func cleanup(paths ...string) error {
    var errs []error
    for _, path := range paths {
        if err := os.Remove(path); err != nil {
            errs = append(errs, fmt.Errorf("remove %s: %w", path, err))
        }
    }
    if len(errs) > 0 {
        return fmt.Errorf("cleanup errors: %v", errs)
    }
    return nil
}

// Pattern 3: Helper for ignoring errors (when appropriate)
func safeClose(closer io.Closer) {
    if err := closer.Close(); err != nil {
        log.Printf("warning: close error: %v", err)
    }
}

// Usage
defer safeClose(file)

// Pattern 4: Error grouping
type ErrorGroup struct {
    errors []error
}

func (eg *ErrorGroup) Add(err error) {
    if err != nil {
        eg.errors = append(eg.errors, err)
    }
}

func (eg *ErrorGroup) Return() error {
    if len(eg.errors) == 0 {
        return nil
    }
    return fmt.Errorf("multiple errors: %v", eg.errors)
}

// Usage
eg := &ErrorGroup{}
eg.Add(file.Close())
eg.Add(os.Remove(tempFile))
return eg.Return()
```

---

## 📊 **PRIORITIZED FIX ORDER**

### Phase 1: Critical (Week 1)
1. **G702, G204** - Command injection (26 issues) - **HIGHEST RISK**
2. **G703, G304** - Path traversal (51 issues) - **HIGH RISK**
3. **G704** - SSRF (6 issues) - **HIGH RISK**

### Phase 2: High (Week 2)
4. **G115** - Integer overflow (1 issue)
5. **G110** - Decompression bomb (1 issue)
6. **G305** - Tar traversal (1 issue)

### Phase 3: Medium (Week 3)
7. **G104** - Unhandled errors (55 issues)
8. **G301, G302, G306** - File permissions (23 issues)

### Phase 4: Low (Week 4)
9. **G117** - Secrets in structs (11 issues)
10. **G107** - HTTP variable URLs (3 issues)
11. **G103** - Unsafe calls (2 issues)

---

## 🛠️ **AUTOMATION SUGGESTIONS**

### Pre-commit Hook
```bash
#!/bin/bash
# .git/hooks/pre-commit

echo "Running security checks..."
gosec ./...
if [ $? -ne 0 ]; then
    echo "❌ Security issues found. Please fix before committing."
    exit 1
fi
```

### CI/CD Integration
```yaml
# .github/workflows/security.yml
name: Security Scan
on: [push, pull_request]

jobs:
  security:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v3
      - name: Run Gosec
        run: |
          go install github.com/securego/gosec/v2/cmd/gosec@latest
          gosec -no-fail -fmt=json -out=results.json ./...
      - name: Upload results
        uses: actions/upload-artifact@v3
        with:
          name: security-results
          path: results.json
```

### Makefile Target
```makefile
.PHONY: security
security:
	@echo "🔍 Running security scan..."
	@gosec ./...
	@echo "✅ Security scan complete"

.PHONY: security-fix
security-fix:
	@echo "🔧 Running security auto-fix..."
	@gosec -no-fail -fmt=json ./... | jq -r '.Issues[] | "\(.file):\(.line) - \(.what)"'
	@echo "Please fix issues manually (auto-fix not available)"
```

---

## 📚 **ADDITIONAL RESOURCES**

1. **OWASP Go Security Cheat Sheet**: https://cheatsheetseries.owasp.org/cheatsheets/Go_Security_Cheat_Sheet.html
2. **Gosec Rules**: https://github.com/securego/gosec#available-rules
3. **Go Security Blog**: https://security.googleblog.com/search/label/golang

Would you like me to provide more detailed fixes for any specific category or help you implement these solutions?
