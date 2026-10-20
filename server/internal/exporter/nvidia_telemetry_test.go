package exporter

import (
	"context"
	"strings"
	"testing"

	pb "vgpu/api/v1"
	"vgpu/internal/biz"
	"vgpu/internal/conf"

	"github.com/prometheus/client_golang/prometheus"
)

func TestUseHAMiNvidiaTelemetry(t *testing.T) {
	tests := []struct {
		source  string
		want    bool
		wantErr bool
	}{
		{source: "", want: false},
		{source: "dcgm", want: false},
		{source: "hami", want: true},
		{source: "HAMi", wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.source, func(t *testing.T) {
			got, err := useHAMiNvidiaTelemetry(&conf.Bootstrap{Exporter: &conf.Exporter{NvidiaTelemetry: tt.source}})
			if (err != nil) != tt.wantErr || got != tt.want {
				t.Fatalf("useHAMiNvidiaTelemetry(%q) = (%t, %v), want (%t, error %t)", tt.source, got, err, tt.want, tt.wantErr)
			}
		})
	}
}

func TestGenerateDeviceMetricsReadsHAMiNvidiaTelemetry(t *testing.T) {
	const (
		deviceID = "GPU-hami-telemetry"
		nodeName = "node-hami-telemetry"
	)
	device := &biz.DeviceInfo{
		Id: deviceID, Devmem: 40960, Devcore: 100, Count: 4,
		Type: "NVIDIA-A100", NodeName: nodeName, Provider: biz.NvidiaGPUDevice,
	}
	labels := []string{nodeName, biz.NvidiaGPUDevice, device.Type, deviceID, "", ""}

	t.Run("host series", func(t *testing.T) {
		generator := newDeviceMetricsTestGenerator(device, map[string]*pb.InstantResponse{
			`avg(hami_host_gpu_memory_used_bytes{device_uuid="GPU-hami-telemetry"})`:   instantValue(10240 * bytesPerMiB),
			`avg(hami_host_gpu_utilization_ratio{device_uuid="GPU-hami-telemetry"})`:   instantValue(37),
			`avg(hami_host_gpu_temperature_celsius{device_uuid="GPU-hami-telemetry"})`: instantValue(55),
			`avg(hami_host_gpu_power_usage_watts{device_uuid="GPU-hami-telemetry"})`:   instantValue(120.5),
		})
		generator.hamiNvidiaTelemetry = true
		t.Cleanup(func() { deleteTrackedTestCells(generator) })

		if err := generator.GenerateDeviceMetrics(context.Background()); err != nil {
			t.Fatalf("GenerateDeviceMetrics() error = %v", err)
		}
		assertTrackedGaugeValue(t, generator, HamiMemoryUsed, labels, 10240)
		assertTrackedGaugeValue(t, generator, HamiMemorySize, labels, 40960)
		assertTrackedGaugeValue(t, generator, HamiMemoryUtil, labels, 25)
		assertTrackedGaugeValue(t, generator, HamiVMemoryScaling, labels, 1)
		assertTrackedGaugeValue(t, generator, HamiCoreUtil, labels, 37)
		assertTrackedGaugeValue(t, generator, HamiCoreUtilAvg, labels, 37)
		assertTrackedGaugeValue(t, generator, HamiDeviceTemperature, labels, 55)
		assertTrackedGaugeValue(t, generator, HamiDevicePower, labels, 120.5)
		for _, gauge := range []*prometheus.GaugeVec{HamiDeviceMemoryTemperature, HamiDeviceFanSpeedP, HamiDeviceLastXIDErrorCode} {
			assertGaugeTracked(t, generator, gauge, labels, false)
		}
		for _, query := range generator.monitorService.(*fakeInstantQuerier).queries {
			if strings.Contains(query, "DCGM_") {
				t.Fatalf("HAMi telemetry issued DCGM query %s", query)
			}
		}
	})

	t.Run("absent temperature and power", func(t *testing.T) {
		generator := newDeviceMetricsTestGenerator(device, map[string]*pb.InstantResponse{})
		generator.hamiNvidiaTelemetry = true
		t.Cleanup(func() { deleteTrackedTestCells(generator) })

		if err := generator.GenerateDeviceMetrics(context.Background()); err != nil {
			t.Fatalf("GenerateDeviceMetrics() error = %v", err)
		}
		assertTrackedGaugeValue(t, generator, HamiMemorySize, labels, 40960)
		assertGaugeTracked(t, generator, HamiDeviceTemperature, labels, false)
		assertGaugeTracked(t, generator, HamiDevicePower, labels, false)
	})
}

func TestGenerateDeviceMetricsDefaultsToDCGMNvidiaTelemetry(t *testing.T) {
	const deviceID = "GPU-dcgm-default"
	generator := newDeviceMetricsTestGenerator(&biz.DeviceInfo{
		Id: deviceID, Devmem: 40960, Devcore: 100, Count: 1,
		Type: "A100", NodeName: "node-dcgm-default", Provider: biz.NvidiaGPUDevice,
	}, map[string]*pb.InstantResponse{})
	t.Cleanup(func() { deleteTrackedTestCells(generator) })

	if err := generator.GenerateDeviceMetrics(context.Background()); err != nil {
		t.Fatalf("GenerateDeviceMetrics() error = %v", err)
	}
	queries := generator.monitorService.(*fakeInstantQuerier).queries
	for _, metric := range []string{"DCGM_FI_DEV_FB_USED", "DCGM_FI_DEV_FB_FREE", "DCGM_FI_DEV_GPU_UTIL", "DCGM_FI_DEV_GPU_TEMP", "DCGM_FI_DEV_POWER_USAGE"} {
		if !containsQuery(queries, metric) {
			t.Fatalf("DCGM telemetry did not query %s; queries = %v", metric, queries)
		}
	}
	if containsQuery(queries, "hami_host_gpu_") {
		t.Fatalf("DCGM telemetry queried HAMi host series; queries = %v", queries)
	}
}

func containsQuery(queries []string, metric string) bool {
	for _, query := range queries {
		if strings.Contains(query, metric) {
			return true
		}
	}
	return false
}
