package main

import (
	"fmt"
	"os"
	"regexp"
	"strings"
)

// raspberryFirmwareUserData is where Raspberry Pi OS (Bookworm+) stores cloud-init user-data.
const raspberryFirmwareUserData = "/boot/firmware/user-data"

var reCloudHostnameLine = regexp.MustCompile(`(?m)^([ \t]*)hostname:\s*.*$`)

// tryUpdateRaspberryUserDataHostname updates or inserts `hostname:` in cloud-init user-data when the file exists.
// Returns (true, nil) if the file was written; (false, nil) if the path does not exist (not an error).
// Returns (_, err) on I/O or unsupported file layout (no hostname line and no #cloud-config).
func tryUpdateRaspberryUserDataHostname(hostname string) (bool, error) {
	fi, err := os.Stat(raspberryFirmwareUserData)
	if err != nil {
		if os.IsNotExist(err) {
			return false, nil
		}
		return false, err
	}
	if !fi.Mode().IsRegular() {
		return false, nil
	}
	b, err := os.ReadFile(raspberryFirmwareUserData)
	if err != nil {
		return false, err
	}
	content := strings.ReplaceAll(string(b), "\r\n", "\n")
	content = strings.ReplaceAll(content, "\r", "\n")
	newContent, ok := replaceOrInsertCloudConfigHostname(content, hostname)
	if !ok {
		return false, fmt.Errorf("%s: no #cloud-config header and no hostname: line to update", raspberryFirmwareUserData)
	}
	if newContent == content {
		return false, nil
	}
	mode := fi.Mode().Perm()
	if mode == 0 {
		mode = 0644
	}
	if err := os.WriteFile(raspberryFirmwareUserData, []byte(newContent), mode); err != nil {
		return false, err
	}
	return true, nil
}

func replaceOrInsertCloudConfigHostname(content, hostname string) (string, bool) {
	if reCloudHostnameLine.MatchString(content) {
		out := reCloudHostnameLine.ReplaceAllString(content, "${1}hostname: "+hostname)
		return out, true
	}
	idx := strings.Index(content, "#cloud-config")
	if idx < 0 {
		return "", false
	}
	rest := content[idx+len("#cloud-config"):]
	lineEnd := strings.Index(rest, "\n")
	if lineEnd < 0 {
		// file ends with #cloud-config and no newline
		return content + "\nhostname: " + hostname + "\n", true
	}
	insertPos := idx + len("#cloud-config") + lineEnd + 1
	return content[:insertPos] + "hostname: " + hostname + "\n" + content[insertPos:], true
}

func raspberryUserDataFilePresent() bool {
	_, err := os.Stat(raspberryFirmwareUserData)
	return err == nil
}
