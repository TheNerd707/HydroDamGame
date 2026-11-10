package internal

import (
	"fmt"
)

func GridUpdateEvent(events chan string, newDemand float64) {
	message := fmt.Sprintf("GRID UPDATE: New grid demand is %.1f MW", newDemand)
	events <- message
}

func OffsidePowerEvent(events chan string) {
	message := "\033[41mALERT: Offside power trip. Shutdown turbines to prevent damage.\033[0m"
	events <- message
}