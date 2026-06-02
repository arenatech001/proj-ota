package main

import (
	"archive/zip"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
)

func parseLogDateRange(dateStart, dateEnd string) error {
	start, err1 := time.ParseInLocation("2006-01-02", strings.TrimSpace(dateStart), time.Local)
	end, err2 := time.ParseInLocation("2006-01-02", strings.TrimSpace(dateEnd), time.Local)
	if err1 != nil || err2 != nil {
		return fmt.Errorf("日期格式无效，请使用 YYYY-MM-DD")
	}
	if start.After(end) {
		return fmt.Errorf("开始日期不能晚于结束日期")
	}
	const maxDays = 90
	if end.Sub(start) > maxDays*24*time.Hour {
		return fmt.Errorf("日期区间不能超过 %d 天", maxDays)
	}
	return nil
}

func addFileToZip(zw *zip.Writer, nameInZip, srcPath string) error {
	fi, err := os.Stat(srcPath)
	if err != nil {
		return err
	}
	if fi.IsDir() {
		return fmt.Errorf("not a file: %s", srcPath)
	}
	hdr, err := zip.FileInfoHeader(fi)
	if err != nil {
		return err
	}
	hdr.Name = filepath.ToSlash(nameInZip)
	hdr.Method = zip.Deflate
	w, err := zw.CreateHeader(hdr)
	if err != nil {
		return err
	}
	f, err := os.Open(srcPath)
	if err != nil {
		return err
	}
	defer f.Close()
	_, err = io.Copy(w, f)
	return err
}

func writeLogZip(w io.Writer, bundle *logFilesBundle) error {
	zw := zip.NewWriter(w)
	if bundle.clientPath != "" {
		if err := addFileToZip(zw, filepath.Join("client", filepath.Base(bundle.clientPath)), bundle.clientPath); err != nil {
			_ = zw.Close()
			return err
		}
	}
	for i, sp := range bundle.serverPaths {
		nameInZip := fmt.Sprintf("server/%02d-%s", i+1, filepath.Base(sp))
		if err := addFileToZip(zw, nameInZip, sp); err != nil {
			_ = zw.Close()
			return err
		}
	}
	for i, ap := range bundle.agentPaths {
		nameInZip := fmt.Sprintf("agent/%02d-%s", i+1, filepath.Base(ap))
		if err := addFileToZip(zw, nameInZip, ap); err != nil {
			_ = zw.Close()
			return err
		}
	}
	return zw.Close()
}

func (s *adminServer) handleAPILogsDownload(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeJSONError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	if !s.requireAuth(w, r) {
		return
	}

	dateStart := strings.TrimSpace(r.URL.Query().Get("date_start"))
	dateEnd := strings.TrimSpace(r.URL.Query().Get("date_end"))
	if dateStart == "" || dateEnd == "" {
		writeJSONError(w, http.StatusBadRequest, "请提供 date_start 与 date_end（YYYY-MM-DD）")
		return
	}
	if err := parseLogDateRange(dateStart, dateEnd); err != nil {
		writeJSONError(w, http.StatusBadRequest, err.Error())
		return
	}

	cfg := s.runtime.get()
	applyAgentDefaults(cfg)
	lu := cfg.LogUpload

	bundle, err := collectLogFiles(lu.ScanDir, lu.ClientGlob, lu.ServerGlob, lu.AgentGlob, dateStart, dateEnd)
	if err != nil {
		writeJSONError(w, http.StatusNotFound, "指定日期区间内未找到匹配的 client、server 或 agent 日志")
		return
	}

	filename := fmt.Sprintf("logs_%s_%s.zip", dateStart, dateEnd)
	w.Header().Set("Content-Type", "application/zip")
	w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="%s"`, filename))

	if err := writeLogZip(w, bundle); err != nil {
		s.logger.Error("log download zip: %v", err)
	}
}
