package fun

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

const (
	PanicLevel uint8 = iota
	ErrorLevel
	WarnLevel
	InfoLevel
	DebugLevel
	TraceLevel
)

var logChan = make(chan string, 100)
var logWg sync.WaitGroup

const (
	TerminalMode uint8 = iota
	FileMode
)

var logMutex sync.Mutex

type Logger struct {
	Level          uint8
	Mode           uint8
	MaxSizeFile    uint8  //文件最大大小(MB)
	MaxNumberFiles uint64 //文件最多数量
	ExpireLogsDays uint8  //文件保留时间
	LogFilePath    string
}

var logger Logger = Logger{
	Level:          TraceLevel,
	Mode:           TerminalMode,
	MaxSizeFile:    0,
	MaxNumberFiles: 0,
	ExpireLogsDays: 0,
	LogFilePath:    "../log",
}

func init() {
	go deleteLogWorker()
	go logWriterWorker()
}

func logWriterWorker() {
	for text := range logChan {
		logMutex.Lock()
		if logger.Mode == FileMode {
			fileLogger(text)
		} else {
			fmt.Println(text)
		}
	}
}

func deleteLogWorker() {
	cleanupExpiredLogs()
	ticker := time.NewTicker(24 * time.Hour)
	defer ticker.Stop()
	for {
		select {
		case <-ticker.C:
			if logger.Mode == FileMode {
				cleanupExpiredLogs()
			}
		}
	}
}

func getLogFilePath() string {
	if logger.LogFilePath == "" {
		return "./log"
	}
	return logger.LogFilePath
}

func cleanupExpiredLogs() {
	if logger.ExpireLogsDays <= 0 {
		return
	}
	_, err := os.Stat(getLogFilePath())
	if os.IsNotExist(err) {
		return
	}
	if err != nil {
		return
	}
	entries, err := os.ReadDir(getLogFilePath())
	if err != nil {
		return
	}
	expireDuration := time.Duration(logger.ExpireLogsDays) * 24 * time.Hour
	currentTimeMillis := time.Now().UnixMilli()
	expireThreshold := currentTimeMillis - expireDuration.Milliseconds()

	for _, entry := range entries {
		if !entry.IsDir() {
			fileNameInfo := getFileNameInfo(entry.Name())
			if fileNameInfo.LoggerTime == 0 {
				continue
			}
			if fileNameInfo.LoggerTime < expireThreshold {
				fullPath := filepath.Join(getLogFilePath(), entry.Name())
				err := os.Remove(fullPath)
				if err != nil && !os.IsNotExist(err) {
					return
				}
			}
		}
	}
}

func getFileNameInfo(name string) fileName {
	fileNameParts := strings.Split(name, ".log.")
	if len(fileNameParts) != 2 {
		deleteLog(name)
		return fileName{}
	}
	dateLayout := "2006-01-02"
	dateString := fileNameParts[0]
	fileDate, err := time.Parse(dateLayout, dateString)
	if err != nil {
		deleteLog(name)
		return fileName{}
	}
	indexString := fileNameParts[1]
	indexString = strings.TrimSuffix(indexString, ".log")
	fileIndex, err := strconv.ParseInt(indexString, 10, 32)
	if err != nil {
		deleteLog(name)
		return fileName{}
	}
	return fileName{
		index:      int32(fileIndex),
		LoggerTime: fileDate.UnixMilli(),
	}
}

type fileName struct {
	LoggerTime int64
	index      int32
}

func deleteLog(name string) {
	fullPath := filepath.Join(getLogFilePath(), name)
	err := os.Remove(fullPath)
	if err != nil && !os.IsNotExist(err) {
		return
	}
}

func fileLogger(text string) {
	_, err := os.Stat(getLogFilePath())
	if os.IsNotExist(err) {
		err = os.MkdirAll(getLogFilePath(), os.ModePerm)
		if err != nil {
			return
		}
	}
	currentDate := getCurrentData()
	logFileName := currentDate + ".log"
	logFilePath := filepath.Join(getLogFilePath(), logFileName)
	logFilePath, err = getNextLogFile(getLogFilePath(), currentDate, text)
	if err != nil {
		return
	}
	file, err := os.OpenFile(logFilePath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0644)
	if err != nil {
		return
	}
	defer func(file *os.File) {
		_ = file.Close()
	}(file)
	_, _ = file.WriteString(text + "\n")
}

func removeOldestLogFile(entries []os.DirEntry) {
	if logger.MaxNumberFiles == 0 {
		return
	}
	if uint64(len(entries)) < logger.MaxNumberFiles {
		return
	}
	var newEntries []fileName
	for _, v := range entries {
		fileNameInfo := getFileNameInfo(v.Name())
		if fileNameInfo.LoggerTime != 0 {
			newEntries = append(newEntries, fileNameInfo)
		}
	}
	if uint64(len(newEntries)) < logger.MaxNumberFiles {
		return
	}
	delNum := uint64(len(newEntries)) - logger.MaxNumberFiles + 1
	sort.Slice(newEntries, func(i, j int) bool {
		if newEntries[i].LoggerTime != newEntries[j].LoggerTime {
			return newEntries[i].LoggerTime < newEntries[j].LoggerTime
		}
		return newEntries[i].index < newEntries[j].index
	})
	for i := 0; i < int(delNum); i++ {
		fileName := newEntries[i]
		t := time.Unix(0, fileName.LoggerTime*int64(time.Millisecond))
		fileNamePath := filepath.Join(getLogFilePath(), t.Format("2006-01-02")+".log."+strconv.Itoa(int(fileName.index)))
		deleteLog(fileNamePath)
	}
}

// getNextLogFile 获取下一个应该写入的日志文件
func getNextLogFile(dirPath, dateStr string, text string) (string, error) {
	entries, err := os.ReadDir(dirPath)
	if err != nil {
		return filepath.Join(dirPath, dateStr+".log.1"), err
	}
	var maxIndex int32 = 0
	var existingFiles []string
	for _, entry := range entries {
		if !entry.IsDir() && strings.HasPrefix(entry.Name(), dateStr+".log") {
			existingFiles = append(existingFiles, entry.Name())
		}
	}
	if len(existingFiles) == 0 {
		removeOldestLogFile(entries)
		return filepath.Join(dirPath, dateStr+".log.1"), nil
	}
	for _, fileName := range existingFiles {
		fileNameInfo := getFileNameInfo(fileName)
		if fileNameInfo.LoggerTime != 0 && fileNameInfo.index > maxIndex {
			maxIndex = fileNameInfo.index
		}
	}
	if maxIndex == 0 {
		removeOldestLogFile(entries)
		return filepath.Join(dirPath, dateStr+".log.1"), nil
	}
	if logger.MaxSizeFile > 0 && maxIndex > 0 {
		currentFile := filepath.Join(dirPath, fmt.Sprintf("%s.log.%d", dateStr, maxIndex))
		if fileInfo, err := os.Stat(currentFile); err == nil {
			maxSizeBytes := int64(logger.MaxSizeFile) * 1024 * 1024
			if fileInfo.Size()+int64(len(text)) > maxSizeBytes {
				removeOldestLogFile(entries)
				return filepath.Join(dirPath, fmt.Sprintf("%s.log.%d", dateStr, maxIndex+1)), nil
			}
		} else {
			return "", err
		}
	}
	return filepath.Join(dirPath, fmt.Sprintf("%s.log.%d", dateStr, maxIndex)), nil
}

func ConfigLogger(log Logger) {
	logger = log
}

func getCurrentTime() string {
	return time.Now().Format("2006-01-02 15:04:05")
}

func getCurrentData() string {
	return time.Now().Format("2006-01-02")
}

func getMethodNameLogger() string {
	pc, _, _, _ := runtime.Caller(3)
	fn := runtime.FuncForPC(pc)
	charsToRemove := []string{"(", "*", ")"}
	name := fn.Name()
	for _, char := range charsToRemove {
		name = strings.ReplaceAll(name, char, "")
	}
	funcName := "[" + padString(strings.ReplaceAll(name, "/", "."), 40) + "] "
	return funcName
}

func getLevelName(level uint8) string {
	switch level {
	case TraceLevel:
		return "TRACE"
	case DebugLevel:
		return "DEBUG"
	case InfoLevel:
		return "INFO"
	case ErrorLevel:
		return "ERROR"
	case WarnLevel:
		return "WARN"
	default:
		return "PANIC"
	}
}

func sendLogWorker(level uint8, message []any) {
	if logger.Level >= level {
		var text1 strings.Builder
		for _, m := range message {
			var msgStr string
			var temp interface{}
			var trimmedStr string
			switch v := m.(type) {
			case string:
				err := json.Unmarshal([]byte(v), &temp)
				if err != nil {
					msgStr = fmt.Sprintf("%s", v)
					break
				}
				bs, _ := json.Marshal(&temp)
				trimmedStr = string(bs)
			case []byte:
				err := json.Unmarshal(v, &temp)
				if err != nil {
					msgStr = fmt.Sprintf("%s", v)
					break
				}
				bs, _ := json.Marshal(&temp)
				trimmedStr = string(bs)
			default:
				bs, _ := json.Marshal(v)
				err := json.Unmarshal(bs, &temp)
				if err != nil {
					msgStr = fmt.Sprintf("%v", v)
					break
				}
				trimmedStr = string(bs)
			}
			switch temp.(type) {
			case map[string]any, []any:
				var out bytes.Buffer
				err := json.Indent(&out, []byte(trimmedStr), "", "\t")
				if err != nil {
					return
				}
				msgStr = fmt.Sprintf("\n%s", out.String())
			default:
				msgStr = fmt.Sprintf("%v", m)
			}
			text1.WriteString(msgStr + " ")
		}
		text := "[" + getCurrentTime() + "] [" + padString(getLevelName(level), 7) + "] " + getMethodNameLogger() + text1.String()
		logWg.Add(1)
		logChan <- text
	}
}

func DebugLogger(message ...any) {
	sendLogWorker(DebugLevel, message)
}

func InfoLogger(message ...any) {
	sendLogWorker(InfoLevel, message)
}

func TraceLogger(message ...any) {
	sendLogWorker(TraceLevel, message)
}

func ErrorLogger(message ...any) {
	sendLogWorker(ErrorLevel, message)
}

func WarnLogger(message ...any) {
	sendLogWorker(WarnLevel, message)
}

func PanicLogger(message ...any) {
	sendLogWorker(PanicLevel, message)
}

func padString(str string, totalLength int) string {
	return fmt.Sprintf("%-*s", totalLength, str)[0:totalLength]
}
