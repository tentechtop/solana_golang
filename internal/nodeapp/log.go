package nodeapp

import (
	"io"
	"os"
	"path/filepath"
	"sync"
)

type rotatingLog struct {
	mu   sync.Mutex
	path string
	file *os.File
}

func newRotatingLog(path string) (*rotatingLog, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return nil, err
	}
	file, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0600)
	if err != nil {
		return nil, err
	}
	return &rotatingLog{path: path, file: file}, nil
}

func (log *rotatingLog) Write(data []byte) (int, error) {
	log.mu.Lock()
	defer log.mu.Unlock()
	if log.file == nil {
		return 0, os.ErrClosed
	}
	if info, err := log.file.Stat(); err == nil && info.Size()+int64(len(data)) > 2<<20 {
		if err = log.file.Close(); err != nil {
			return 0, err
		}
		_ = os.Remove(log.path + ".2")
		_ = os.Rename(log.path+".1", log.path+".2")
		_ = os.Rename(log.path, log.path+".1")
		log.file = nil
		log.file, err = os.OpenFile(log.path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0600)
		if err != nil {
			return 0, err
		}
	}
	return log.file.Write(data)
}

func (log *rotatingLog) Tail(limit int64) string {
	log.mu.Lock()
	defer log.mu.Unlock()
	file, err := os.Open(log.path)
	if err != nil {
		return "暂无日志"
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		return "日志读取失败"
	}
	if info.Size() > limit {
		_, _ = file.Seek(-limit, io.SeekEnd)
	}
	data, _ := io.ReadAll(io.LimitReader(file, limit))
	return string(data)
}

func (log *rotatingLog) Close() {
	log.mu.Lock()
	defer log.mu.Unlock()
	if log.file != nil {
		_ = log.file.Close()
		log.file = nil
	}
}
