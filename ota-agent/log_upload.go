package main

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

type logJobPayload struct {
	ID          int    `json:"id"`
	Location    string `json:"location"`
	AgentID     string `json:"agent_id"`
	DateStart   string `json:"date_start"`
	DateEnd     string `json:"date_end"`
	UploadToken string `json:"upload_token"`
}

type logNextResponse struct {
	Job *logJobPayload `json:"job"`
}

func trimSlash(s string) string {
	return strings.TrimRight(strings.TrimSpace(s), "/")
}

func fileInDateRange(info os.FileInfo, dateStart, dateEnd string) bool {
	start, err1 := time.ParseInLocation("2006-01-02", dateStart, time.Local)
	end, err2 := time.ParseInLocation("2006-01-02", dateEnd, time.Local)
	if err1 != nil || err2 != nil {
		return false
	}
	end = end.Add(24*time.Hour - time.Nanosecond)
	mt := info.ModTime()
	return !mt.Before(start) && !mt.After(end)
}

func pickLogFile(scanDir, pattern, dateStart, dateEnd string) (string, error) {
	globPath := filepath.Join(scanDir, pattern)
	matches, err := filepath.Glob(globPath)
	if err != nil {
		return "", err
	}
	var best string
	var bestTime time.Time
	for _, m := range matches {
		info, err := os.Stat(m)
		if err != nil || info.IsDir() {
			continue
		}
		if !fileInDateRange(info, dateStart, dateEnd) {
			continue
		}
		if best == "" || info.ModTime().After(bestTime) {
			best = m
			bestTime = info.ModTime()
		}
	}
	if best == "" {
		return "", fmt.Errorf("no file matching glob %s with mtime in [%s,%s]", globPath, dateStart, dateEnd)
	}
	return best, nil
}

// pickAllLogFiles 返回目录下匹配 glob 且 mtime 在区间内的所有普通文件（按路径排序）。
func pickAllLogFiles(scanDir, pattern, dateStart, dateEnd string) ([]string, error) {
	globPath := filepath.Join(scanDir, pattern)
	matches, err := filepath.Glob(globPath)
	if err != nil {
		return nil, err
	}
	var out []string
	for _, m := range matches {
		info, err := os.Stat(m)
		if err != nil || info.IsDir() {
			continue
		}
		if !fileInDateRange(info, dateStart, dateEnd) {
			continue
		}
		out = append(out, m)
	}
	sort.Strings(out)
	return out, nil
}

type logFilesBundle struct {
	clientPath  string
	serverPaths []string
	agentPaths  []string
}

func (b *logFilesBundle) allPaths() []string {
	if b == nil {
		return nil
	}
	var out []string
	if b.clientPath != "" {
		out = append(out, b.clientPath)
	}
	out = append(out, b.serverPaths...)
	out = append(out, b.agentPaths...)
	return out
}

func collectLogFiles(scanDir, clientGlob, serverGlob, agentGlob, dateStart, dateEnd string) (*logFilesBundle, error) {
	var clientPath string
	if cp, e := pickLogFile(scanDir, clientGlob, dateStart, dateEnd); e == nil {
		clientPath = cp
	}

	var serverPaths []string
	if strings.TrimSpace(serverGlob) != "" {
		sp, e := pickAllLogFiles(scanDir, serverGlob, dateStart, dateEnd)
		if e != nil {
			return nil, e
		}
		serverPaths = sp
	}

	var agentPaths []string
	if strings.TrimSpace(agentGlob) != "" {
		ap, e := pickAllLogFiles(scanDir, agentGlob, dateStart, dateEnd)
		if e != nil {
			return nil, e
		}
		agentPaths = ap
	}

	if clientPath == "" && len(serverPaths) == 0 && len(agentPaths) == 0 {
		return nil, fmt.Errorf("no client file under %s/%s and no server files under %s/%s and no agent files under %s/%s in [%s,%s]",
			scanDir, clientGlob, scanDir, serverGlob, scanDir, agentGlob, dateStart, dateEnd)
	}
	return &logFilesBundle{
		clientPath:  clientPath,
		serverPaths: serverPaths,
		agentPaths:  agentPaths,
	}, nil
}

func addFileToTar(tw *tar.Writer, nameInTar, src string) error {
	fi, err := os.Stat(src)
	if err != nil {
		return err
	}
	f, err := os.Open(src)
	if err != nil {
		return err
	}
	defer f.Close()
	hdr, err := tar.FileInfoHeader(fi, "")
	if err != nil {
		return err
	}
	hdr.Name = nameInTar
	hdr.Size = fi.Size()
	hdr.Mode = 0644
	if err := tw.WriteHeader(hdr); err != nil {
		return err
	}
	_, err = io.Copy(tw, f)
	return err
}

// prepareUploadPayload 组装待上传文件：client / server / agent 日志；多文件时合并为临时 tar.gz。
func prepareUploadPayload(
	scanDir, clientGlob, serverGlob, agentGlob string,
	dateStart, dateEnd string,
) (path string, cleanup func(), err error) {
	cleanup = func() {}

	bundle, err := collectLogFiles(scanDir, clientGlob, serverGlob, agentGlob, dateStart, dateEnd)
	if err != nil {
		return "", cleanup, err
	}
	paths := bundle.allPaths()
	if len(paths) == 1 {
		return paths[0], cleanup, nil
	}

	tmp, err := os.CreateTemp("", "ota-log-bundle-*.tar.gz")
	if err != nil {
		return "", cleanup, err
	}
	tmpPath := tmp.Name()
	cleanup = func() { _ = os.Remove(tmpPath) }

	gw := gzip.NewWriter(tmp)
	tw := tar.NewWriter(gw)

	if bundle.clientPath != "" {
		if err := addFileToTar(tw, filepath.ToSlash(filepath.Join("client", filepath.Base(bundle.clientPath))), bundle.clientPath); err != nil {
			cleanup()
			return "", func() {}, err
		}
	}
	for i, sp := range bundle.serverPaths {
		nameInTar := fmt.Sprintf("server/%02d-%s", i+1, filepath.Base(sp))
		if err := addFileToTar(tw, filepath.ToSlash(nameInTar), sp); err != nil {
			cleanup()
			return "", func() {}, err
		}
	}
	for i, ap := range bundle.agentPaths {
		nameInTar := fmt.Sprintf("agent/%02d-%s", i+1, filepath.Base(ap))
		if err := addFileToTar(tw, filepath.ToSlash(nameInTar), ap); err != nil {
			cleanup()
			return "", func() {}, err
		}
	}

	if err := tw.Close(); err != nil {
		cleanup()
		return "", func() {}, err
	}
	if err := gw.Close(); err != nil {
		cleanup()
		return "", func() {}, err
	}
	if err := tmp.Close(); err != nil {
		cleanup()
		return "", func() {}, err
	}

	st, err := os.Stat(tmpPath)
	if err != nil {
		cleanup()
		return "", func() {}, err
	}
	if st.Size() == 0 {
		cleanup()
		return "", func() {}, fmt.Errorf("empty bundle")
	}

	return tmpPath, cleanup, nil
}

func reportJobFailed(baseURL string, jobID int, token, location, agentID, msg string, timeout time.Duration, logger *Logger) error {
	u := fmt.Sprintf("%s/logs/jobs/%d/report", trimSlash(baseURL), jobID)
	body, _ := json.Marshal(map[string]string{"status": "failed", "error": msg, "location": location, "agent_id": agentID})
	req, err := http.NewRequest(http.MethodPost, u, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("X-Location", location)
	req.Header.Set("X-Agent-ID", agentID)
	client := &http.Client{Timeout: timeout}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		b, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("report failed: %d %s", resp.StatusCode, string(b))
	}
	return nil
}

func uploadLogFile(baseURL string, job *logJobPayload, filePath, location, agentID string, maxBytes int64, timeout time.Duration, logger *Logger) error {
	f, err := os.Open(filePath)
	if err != nil {
		return err
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil {
		return err
	}
	if st.Size() > maxBytes {
		return fmt.Errorf("file too large: %d > %d", st.Size(), maxBytes)
	}
	q := url.Values{}
	q.Set("token", job.UploadToken)
	q.Set("location", location)
	q.Set("agent_id", agentID)
	u := fmt.Sprintf("%s/logs/jobs/%d/upload?%s", trimSlash(baseURL), job.ID, q.Encode())
	req, err := http.NewRequest(http.MethodPost, u, f)
	if err != nil {
		return err
	}
	req.ContentLength = st.Size()
	req.Header.Set("Content-Type", "application/octet-stream")
	req.Header.Set("X-Location", location)
	req.Header.Set("X-Agent-ID", agentID)
	client := &http.Client{Timeout: timeout}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != 200 {
		return fmt.Errorf("upload HTTP %d: %s", resp.StatusCode, string(b))
	}
	return nil
}

func pollNextLogJob(baseURL, location, agentID string, timeout time.Duration) (*logJobPayload, error) {
	u := fmt.Sprintf("%s/logs/agent/next", trimSlash(baseURL))
	req, err := http.NewRequest(http.MethodGet, u, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("X-Location", location)
	req.Header.Set("X-Agent-ID", agentID)
	client := &http.Client{Timeout: timeout}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("poll HTTP %d: %s", resp.StatusCode, string(body))
	}
	var out logNextResponse
	if err := json.Unmarshal(body, &out); err != nil {
		return nil, err
	}
	return out.Job, nil
}

func runLogUploadLoop(
	baseURL, location, agentID string,
	scanDir, clientGlob, serverGlob, agentGlob string,
	pollInterval, httpTimeout, uploadTimeout time.Duration,
	maxUploadBytes int64,
	maxReportRetries int,
	logger *Logger,
) {
	if baseURL == "" || location == "" || agentID == "" {
		return
	}
	logger.Info("log upload loop: base=%s location=%s scan=%s client=%s server=%s agent=%s",
		baseURL, location, scanDir, clientGlob, serverGlob, agentGlob)
	ticker := time.NewTicker(pollInterval)
	defer ticker.Stop()
	for range ticker.C {
		job, err := pollNextLogJob(baseURL, location, agentID, httpTimeout)
		if err != nil {
			logger.Error("log job poll: %v", err)
			continue
		}
		if job == nil {
			continue
		}
		logger.Info("log job claimed: id=%d range=%s..%s", job.ID, job.DateStart, job.DateEnd)
		path, cleanup, err := prepareUploadPayload(scanDir, clientGlob, serverGlob, agentGlob, job.DateStart, job.DateEnd)
		if err != nil {
			logger.Warn("log job %d: no payload: %v", job.ID, err)
			for i := 0; i < maxReportRetries; i++ {
				if e := reportJobFailed(baseURL, job.ID, job.UploadToken, location, agentID, err.Error(), httpTimeout, logger); e == nil {
					break
				}
				time.Sleep(time.Duration(i+1) * time.Second)
			}
			continue
		}
		logger.Info("log job %d: uploading %s", job.ID, path)
		upErr := uploadLogFile(baseURL, job, path, location, agentID, maxUploadBytes, uploadTimeout, logger)
		cleanup()
		if upErr != nil {
			logger.Error("log job %d upload failed: %v", job.ID, upErr)
			_ = reportJobFailed(baseURL, job.ID, job.UploadToken, location, agentID, upErr.Error(), httpTimeout, logger)
		} else {
			logger.Info("log job %d upload ok", job.ID)
		}
	}
}
