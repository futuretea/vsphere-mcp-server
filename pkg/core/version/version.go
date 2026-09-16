package version

import "fmt"

const BinaryName = "vsphere-mcp-server"

var (
	Version = "dev"
	Commit  = "unknown"
	Date    = "unknown"
)

// GetVersionInfo returns human-readable build metadata.
func GetVersionInfo() string {
	return fmt.Sprintf("%s version=%s commit=%s date=%s", BinaryName, Version, Commit, Date)
}
