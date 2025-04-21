package main

import (
	"bufio"
	"fmt"
	"io"
	"net/http"
	"os"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"
)

var (
	urlFile       = "/data/url.txt"
	speedLimit    = 200 // KB/s
	threadCount   = 2
	sleepInterval = 5  // 秒
	client        = &http.Client{Timeout: 60 * time.Second}
	urls          []string
	currentUrl    string
	urlLock       sync.RWMutex
)

func logInfo(msg string) {
	fmt.Printf("%s ** [INFO]: %s\n", time.Now().Format("2006/01/02 15:04:05"), msg)
}

func logError(msg string) {
	fmt.Printf("%s ** [ERROR]: %s\n", time.Now().Format("2006/01/02 15:04:05"), msg)
}

func getEnvInt(key string, defaultVal int) int {
	val := os.Getenv(key)
	if val == "" {
		return defaultVal
	}
	if i, err := strconv.Atoi(val); err == nil {
		return i
	}
	return defaultVal
}

func loadUrls(path string) ([]string, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()

	var urls []string
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line != "" {
			urls = append(urls, line)
		}
	}
	return urls, scanner.Err()
}

func download(id int, url string) {
	urlLock.Lock()
	currentUrl = url
	urlLock.Unlock()

	resp, err := client.Get(url)
	if err != nil {
		logError(fmt.Sprintf("线程 %d 下载失败: %v", id, err))
		return
	}
	defer resp.Body.Close()

	limitBytes := int64(speedLimit * 1024)
	buf := make([]byte, 1024)
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()

	readBytes := int64(0)
	for {
		n, err := resp.Body.Read(buf)
		if n > 0 {
			readBytes += int64(n)
			_, _ = io.Discard.Write(buf[:n])
		}
		if readBytes >= limitBytes {
			<-ticker.C
			readBytes = 0
		}
		if err != nil {
			if err != io.EOF {
				logError(fmt.Sprintf("线程 %d 下载中断: %v", id, err))
			}
			break
		}
	}

	runtime.GC() // 触发垃圾回收
}

func getLoadAvg() string {
	data, err := os.ReadFile("/proc/loadavg")
	if err != nil {
		return "未知"
	}
	fields := strings.Fields(string(data))
	if len(fields) >= 3 {
		return fmt.Sprintf("%s %s %s", fields[0], fields[1], fields[2])
	}
	return "未知"
}

func getCpuUsage() (float64, error) {
	data, err := os.ReadFile("/proc/stat")
	if err != nil {
		return 0, err
	}
	fields := strings.Fields(string(data))
	if len(fields) < 5 {
		return 0, fmt.Errorf("unexpected /proc/stat format")
	}

	var idle, total uint64
	for i, val := range fields[1:] {
		num, err := strconv.ParseUint(val, 10, 64)
		if err != nil {
			return 0, err
		}
		total += num
		if i == 3 {
			idle = num
		}
	}

	used := total - idle
	return (float64(used) / float64(total)) * 100, nil
}

type NetStats struct {
	recvBytes uint64
	sendBytes uint64
}

func getNetStats() (NetStats, error) {
	data, err := os.ReadFile("/proc/net/dev")
	if err != nil {
		return NetStats{}, err
	}

	lines := strings.Split(string(data), "\n")
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "eth0:") {
			fields := strings.Fields(line)
			if len(fields) < 17 {
				return NetStats{}, nil
			}
			recvBytes, _ := strconv.ParseUint(fields[1], 10, 64)
			sendBytes, _ := strconv.ParseUint(fields[9], 10, 64)
			return NetStats{recvBytes, sendBytes}, nil
		}
	}
	return NetStats{}, nil
}

func monitor() {
	var lastStats NetStats
	lastStats, _ = getNetStats()

	for {
		var memStats runtime.MemStats
		runtime.ReadMemStats(&memStats)

		memMB := float64(memStats.Alloc) / 1024 / 1024
		loadAvg := getLoadAvg()
		cpuUsage, _ := getCpuUsage()
		currentStats, _ := getNetStats()

		recvSpeed := float64(currentStats.recvBytes-lastStats.recvBytes) / float64(sleepInterval) / 1024 / 1024
		sendSpeed := float64(currentStats.sendBytes-lastStats.sendBytes) / float64(sleepInterval) / 1024 / 1024

		lastStats = currentStats

		urlLock.RLock()
		current := currentUrl
		urlLock.RUnlock()

		currentTime := time.Now().Format("2006/01/02 15:04:05")
		fmt.Printf("%s ** [INFO]: ******************************************************************************************\n", currentTime)
		fmt.Printf("%s ** [INFO]: ** 资源地址: %s\n", currentTime, current)
		fmt.Printf("%s ** [INFO]: ** 并发线程: %d\n", currentTime, threadCount)
		fmt.Printf("%s ** [INFO]: ** 内存占用: %.2fMB\n", currentTime, memMB)
		fmt.Printf("%s ** [INFO]: ** 处理器占用: %.3f%%\n", currentTime, cpuUsage)
		fmt.Printf("%s ** [INFO]: ** 平均负载占用: %s\n", currentTime, loadAvg)
		fmt.Printf("%s ** [INFO]: ** 网口: eth0 下行: %.3fGB(%.3fMB/s) 上行: %.3fMB(%.3fMB/s)\n",
			currentTime,
			float64(currentStats.recvBytes)/(1024*1024*1024), recvSpeed,
			float64(currentStats.sendBytes)/(1024*1024), sendSpeed,
		)
		fmt.Printf("%s ** [INFO]: ******************************************************************************************\n", currentTime)

		time.Sleep(time.Duration(sleepInterval) * time.Second)
	}
}

func main() {
	if env := os.Getenv("URL_FILE"); env != "" {
		urlFile = env
	}
	speedLimit = getEnvInt("DOWNLOAD_SPEED_LIMIT", 200)
	threadCount = getEnvInt("THREADS", 2)
	sleepInterval = getEnvInt("SLEEP_INTERVAL", 5)

	var err error
	urls, err = loadUrls(urlFile)
	if err != nil {
		logError(fmt.Sprintf("加载URL失败: %v", err))
		os.Exit(1)
	}
	if len(urls) == 0 {
		logError("URL列表为空，退出！")
		os.Exit(1)
	}

	logInfo(fmt.Sprintf("加载到 %d 个URL，线程数: %d，限速: %dKB/s，监控间隔: %d秒", len(urls), threadCount, speedLimit, sleepInterval))

	go monitor()

	var wg sync.WaitGroup
	for i := 0; i < threadCount; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			idx := id
			for {
				url := urls[idx%len(urls)]
				download(id, url)
				idx += threadCount
			}
		}(i)
	}

	wg.Wait()
}
