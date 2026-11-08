package internal

import (
	"math"
)

func DepthtoVolumeKM3(depth float64, maxDepth float64, maxVolume float64) float64 {
	return maxVolume * (depth / maxDepth) * ( depth / maxDepth);
}

func VolumetoDepthM(volume float64, maxDepth float64, maxVolume float64) float64 {
	return maxDepth * math.Sqrt(volume/maxVolume)
}

func DepthChangeRateMs(maxVolumeKM3, maxDepth, currentDepth, volumeChangeRate float64) float64 {
	if currentDepth <= 0 {
		return 0
	}

	maxVolumeM3 := maxVolumeKM3 * 1e9

	return (maxDepth * maxDepth * volumeChangeRate) / (2 * maxVolumeM3 * currentDepth)
}

func CalcTurbineFlow(rotation, maxRotation, maxFlow, netHead, ratedHead float64) float64 {
	if rotation <= 0 || netHead <= 0 {
		return 0
	}
	
	if maxRotation <= 0 || ratedHead <= 0 || maxFlow <= 0{
		return 0
	}


	gateratio := math.Min(rotation / maxRotation, 1)
	headRatio := math.Sqrt(netHead/ratedHead)
	
	flow := maxFlow * math.Pow(gateratio, 2) * headRatio

	return math.Min(flow, maxFlow)
}