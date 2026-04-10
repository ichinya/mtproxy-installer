package version

import "strings"

var (
	Version   = "dev"
	Commit    = "unknown"
	BuildDate = "unknown"
	BuildMode = ""
)

type Info struct {
	Version   string
	Commit    string
	BuildDate string
	BuildMode string
}

func Current() Info {
	info := Info{
		Version:   strings.TrimSpace(Version),
		Commit:    strings.TrimSpace(Commit),
		BuildDate: strings.TrimSpace(BuildDate),
		BuildMode: strings.TrimSpace(BuildMode),
	}

	if info.Version == "" {
		info.Version = "dev"
	}
	if info.Commit == "" {
		info.Commit = "unknown"
	}
	if info.BuildDate == "" {
		info.BuildDate = "unknown"
	}
	if info.BuildMode == "" {
		if info.Version == "dev" {
			info.BuildMode = "development"
		} else {
			info.BuildMode = "production"
		}
	}

	return info
}

func (i Info) IsDevelopment() bool {
	mode := strings.ToLower(strings.TrimSpace(i.BuildMode))
	switch mode {
	case "dev", "development":
		return true
	default:
		return i.Version == "dev"
	}
}

func (i Info) StartupMode() string {
	if i.IsDevelopment() {
		return "development"
	}
	return "production"
}
