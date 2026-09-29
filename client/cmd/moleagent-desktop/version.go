package main

import "moleAgent_client/internal/version"

func desktopVersionMap() map[string]string {
	info := version.GetSystemInfo()
	return map[string]string{
		"version":    info.Version,
		"git_hash":   info.GitHash,
		"build_date": info.BuildDate,
	}
}
