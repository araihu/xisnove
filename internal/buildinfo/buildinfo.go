package buildinfo

import (
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"
)

var (
	Version   string
	Commit    string
	BuildDate string
	Dirty     string
)

var (
	semanticVersion = regexp.MustCompile(`^(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)(-[0-9A-Za-z-]+(?:\.[0-9A-Za-z-]+)*)?$`)
	commitHash      = regexp.MustCompile(`^[0-9A-Fa-f]{40}$`)
)

func String(binary string) (string, error) {
	if !semanticVersion.MatchString(Version) {
		return "", errors.New("invalid build version")
	}
	if !commitHash.MatchString(Commit) {
		return "", errors.New("invalid build commit")
	}
	parsedDate, err := time.Parse(time.RFC3339, BuildDate)
	if err != nil || !strings.HasSuffix(BuildDate, "Z") || parsedDate.Location() != time.UTC {
		return "", errors.New("invalid build date")
	}
	if Dirty != "false" {
		return "", errors.New("invalid build dirty state")
	}
	return fmt.Sprintf("%s version=%s commit=%s build_date=%s dirty=%s", binary, Version, Commit, BuildDate, Dirty), nil
}
