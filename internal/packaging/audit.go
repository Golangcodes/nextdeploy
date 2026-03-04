package packaging

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
)

type SizeReport struct {
	TotalMB       float64
	NodeModulesMB float64
	ServerCodeMB  float64
	TopOffenders  []OffenderEntry
}

type OffenderEntry struct {
	Package string
	SizeMB  float64
}

func AuditStandaloneSize(standaloneDir string) (*SizeReport, error) {
	report := &SizeReport{}
	moduleSizes := map[string]int64{}

	err := filepath.Walk(standaloneDir, func(path string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() {
			return err
		}

		rel, _ := filepath.Rel(standaloneDir, path)
		sizeMB := float64(info.Size()) / (1024 * 1024)
		report.TotalMB += sizeMB

		if strings.Contains(rel, "node_modules/") {
			report.NodeModulesMB += sizeMB
			parts := strings.SplitN(rel, "node_modules/", 2)
			if len(parts) == 2 {
				pkgParts := strings.Split(parts[1], string(os.PathSeparator))
				pkg := pkgParts[0]
				// Handle scoped packages
				if strings.HasPrefix(pkg, "@") && len(pkgParts) > 1 {
					pkg = pkg + "/" + pkgParts[1]
				}
				moduleSizes[pkg] += info.Size()
			}
		} else {
			report.ServerCodeMB += sizeMB
		}

		return nil
	})
	if err != nil {
		return nil, err
	}

	for pkg, size := range moduleSizes {
		report.TopOffenders = append(report.TopOffenders, OffenderEntry{
			Package: pkg,
			SizeMB:  float64(size) / (1024 * 1024),
		})
	}
	sort.Slice(report.TopOffenders, func(i, j int) bool {
		return report.TopOffenders[i].SizeMB > report.TopOffenders[j].SizeMB
	})
	if len(report.TopOffenders) > 10 {
		report.TopOffenders = report.TopOffenders[:10]
	}

	return report, nil
}
