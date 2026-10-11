package main

import (
	"bytes"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"net/http"
	"time"

	"gridpulse/internal/telemetry"
)

func main() {
	url := flag.String("url", "http://localhost:8090", "API base URL")
	interval := flag.Duration("interval", time.Second, "time between synthetic readings")
	flag.Parse()
	var seed [6]byte
	if _, err := rand.Read(seed[:]); err != nil {
		log.Fatal(err)
	}
	runID := hex.EncodeToString(seed[:])
	client := &http.Client{Timeout: 5 * time.Second}
	log.Printf("synthetic telemetry run %s; Ctrl-C to stop", runID)
	for sequence := 0; ; sequence++ {
		// Every 30 samples, simulate a 7-sample hot period that opens an alert.
		hot := sequence%30 >= 15 && sequence%30 < 22
		temp := float64(64 + (sequence % 5))
		if hot {
			temp = 90 + float64(sequence%4)
		}
		reading := telemetry.Reading{SampleID: fmt.Sprintf("%s-%06d", runID, sequence), EquipmentID: "lab-unit-01", RecordedAt: time.Now().UTC().Format(time.RFC3339), Voltage: 480 + float64(sequence%7-3), Current: 120 + float64(sequence%11), Temperature: temp, Efficiency: 96.5}
		body, _ := json.Marshal(reading)
		response, err := client.Post(*url+"/api/readings", "application/json", bytes.NewReader(body))
		if err != nil {
			log.Printf("send failed: %v", err)
		} else {
			response.Body.Close()
			if response.StatusCode != http.StatusOK {
				log.Printf("API returned %s", response.Status)
			}
		}
		time.Sleep(*interval)
	}
}
