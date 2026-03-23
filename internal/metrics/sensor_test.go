//go:build darwin

package metrics

import (
	"testing"
)

func TestSensorData(t *testing.T) {
	data := getSensorData()
	t.Logf("SensorData: %+v", data)
}
