package main

import "time"

func downloadStats(downloaded, total int64, startedAt time.Time) (speedBytesPerSecond, elapsedSeconds, remainingSeconds int64) {
	elapsed := time.Since(startedAt)
	if elapsed < 0 {
		elapsed = 0
	}

	elapsedSeconds = int64(elapsed.Seconds())
	if downloaded <= 0 || elapsed <= 0 {
		return 0, elapsedSeconds, 0
	}

	speedBytesPerSecond = int64(float64(downloaded) / elapsed.Seconds())
	if speedBytesPerSecond <= 0 || total <= downloaded {
		return speedBytesPerSecond, elapsedSeconds, 0
	}

	remainingSeconds = (total - downloaded + speedBytesPerSecond - 1) / speedBytesPerSecond
	return speedBytesPerSecond, elapsedSeconds, remainingSeconds
}
