package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func mockInstancesServer(t *testing.T) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		// List instances
		case r.Method == "GET" && r.URL.Path == "/api/v1/instances":
			json.NewEncoder(w).Encode(map[string]any{
				"data": []Instance{
					{ID: "inst-1", Name: "my-gpu", Status: "running", GPUType: "A100", GPUCount: 1, IP: "10.0.0.1", CostPerHour: 1.42},
					{ID: "inst-2", Name: "train-box", Status: "deploying", GPUType: "H100", GPUCount: 8, CostPerHour: 12.0},
				},
			})

		// List types
		case r.Method == "GET" && r.URL.Path == "/api/v1/instances/types":
			gpu := r.URL.Query().Get("gpu_type")
			types := []InstanceType{
				{ID: "type-a100", GPUType: "A100", GPUCount: 1, CPUCores: 30, MemoryGB: 200, StorageGB: 512, Region: "mumbai-india-1", HourlyRate: 1.42},
				{ID: "type-h100", GPUType: "H100", GPUCount: 1, CPUCores: 32, MemoryGB: 256, StorageGB: 1024, Region: "us-east-1", HourlyRate: 3.50},
			}
			if gpu != "" {
				filtered := []InstanceType{}
				for _, t := range types {
					if t.GPUType == gpu {
						filtered = append(filtered, t)
					}
				}
				types = filtered
			}
			json.NewEncoder(w).Encode(map[string]any{"data": types})

		// Get instance
		case r.Method == "GET" && r.URL.Path == "/api/v1/instances/inst-1":
			json.NewEncoder(w).Encode(map[string]any{
				"data": Instance{ID: "inst-1", Name: "my-gpu", Status: "running", GPUType: "A100", GPUCount: 1, IP: "10.0.0.1", CostPerHour: 1.42, Region: "mumbai"},
			})

		// Get instance status
		case r.Method == "GET" && r.URL.Path == "/api/v1/instances/inst-1/status":
			json.NewEncoder(w).Encode(map[string]any{
				"data": InstanceStatus{ID: "inst-1", Status: "running", IP: "10.0.0.1"},
			})

		// Create instance
		case r.Method == "POST" && r.URL.Path == "/api/v1/instances":
			var req CreateInstanceRequest
			json.NewDecoder(r.Body).Decode(&req)
			w.WriteHeader(http.StatusCreated)
			json.NewEncoder(w).Encode(map[string]any{
				"data": Instance{ID: "inst-new", Name: req.Name, Status: "creating", GPUType: req.GPUType, GPUCount: 1, CostPerHour: 1.42},
			})

		// Delete instance
		case r.Method == "DELETE" && r.URL.Path == "/api/v1/instances/inst-1":
			w.WriteHeader(http.StatusNoContent)

		// SSH certificate
		case r.Method == "POST" && r.URL.Path == "/api/v1/instances/inst-1/ssh-certificate":
			json.NewEncoder(w).Encode(map[string]any{
				"data": SSHCertificateResponse{Certificate: "ssh-ed25519-cert-v01@openssh.com AAAA...", IP: "10.0.0.1", User: "root", Port: 22},
			})

		// Not found
		case r.Method == "GET" && r.URL.Path == "/api/v1/instances/not-found":
			w.WriteHeader(http.StatusNotFound)
			json.NewEncoder(w).Encode(map[string]any{"error": map[string]string{"code": "NOT_FOUND", "message": "Instance not found"}})

		// Delete not found
		case r.Method == "DELETE" && r.URL.Path == "/api/v1/instances/not-found":
			w.WriteHeader(http.StatusNotFound)
			json.NewEncoder(w).Encode(map[string]any{"error": map[string]string{"code": "NOT_FOUND", "message": "Instance not found"}})

		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
}

func TestListInstances(t *testing.T) {
	server := mockInstancesServer(t)
	defer server.Close()
	client := NewClient("rc_live_test", server.URL)

	instances, err := client.ListInstances("")
	if err != nil {
		t.Fatalf("ListInstances() error: %v", err)
	}
	if len(instances) != 2 {
		t.Fatalf("got %d instances, want 2", len(instances))
	}
	if instances[0].Name != "my-gpu" {
		t.Errorf("first instance name = %q, want %q", instances[0].Name, "my-gpu")
	}
	if instances[1].Status != "deploying" {
		t.Errorf("second instance status = %q, want %q", instances[1].Status, "deploying")
	}
}

func TestListInstanceTypes(t *testing.T) {
	server := mockInstancesServer(t)
	defer server.Close()
	client := NewClient("rc_live_test", server.URL)

	// Unfiltered
	types, err := client.ListInstanceTypes("", "", 0)
	if err != nil {
		t.Fatalf("ListInstanceTypes() error: %v", err)
	}
	if len(types) != 2 {
		t.Fatalf("got %d types, want 2", len(types))
	}

	// Filtered by GPU
	types, err = client.ListInstanceTypes("A100", "", 0)
	if err != nil {
		t.Fatalf("ListInstanceTypes(A100) error: %v", err)
	}
	if len(types) != 1 {
		t.Fatalf("got %d types for A100, want 1", len(types))
	}
	if types[0].GPUType != "A100" {
		t.Errorf("filtered type = %q, want A100", types[0].GPUType)
	}
}

func TestGetInstance(t *testing.T) {
	server := mockInstancesServer(t)
	defer server.Close()
	client := NewClient("rc_live_test", server.URL)

	inst, err := client.GetInstance("inst-1")
	if err != nil {
		t.Fatalf("GetInstance() error: %v", err)
	}
	if inst.Name != "my-gpu" {
		t.Errorf("name = %q, want %q", inst.Name, "my-gpu")
	}
	if inst.IP != "10.0.0.1" {
		t.Errorf("IP = %q, want %q", inst.IP, "10.0.0.1")
	}
}

func TestGetInstance_NotFound(t *testing.T) {
	server := mockInstancesServer(t)
	defer server.Close()
	client := NewClient("rc_live_test", server.URL)

	_, err := client.GetInstance("not-found")
	if err == nil {
		t.Fatal("expected error for not-found instance, got nil")
	}
}

func TestGetInstanceStatus(t *testing.T) {
	server := mockInstancesServer(t)
	defer server.Close()
	client := NewClient("rc_live_test", server.URL)

	status, err := client.GetInstanceStatus("inst-1")
	if err != nil {
		t.Fatalf("GetInstanceStatus() error: %v", err)
	}
	if status.Status != "running" {
		t.Errorf("status = %q, want %q", status.Status, "running")
	}
	if status.IP != "10.0.0.1" {
		t.Errorf("IP = %q, want %q", status.IP, "10.0.0.1")
	}
}

func TestCreateInstance(t *testing.T) {
	server := mockInstancesServer(t)
	defer server.Close()
	client := NewClient("rc_live_test", server.URL)

	inst, err := client.CreateInstance(CreateInstanceRequest{
		Name:    "new-instance",
		GPUType: "A100",
	})
	if err != nil {
		t.Fatalf("CreateInstance() error: %v", err)
	}
	if inst.ID != "inst-new" {
		t.Errorf("ID = %q, want %q", inst.ID, "inst-new")
	}
	if inst.Name != "new-instance" {
		t.Errorf("name = %q, want %q", inst.Name, "new-instance")
	}
	if inst.Status != "creating" {
		t.Errorf("status = %q, want %q", inst.Status, "creating")
	}
}

func TestDeleteInstance(t *testing.T) {
	server := mockInstancesServer(t)
	defer server.Close()
	client := NewClient("rc_live_test", server.URL)

	err := client.DeleteInstance("inst-1")
	if err != nil {
		t.Errorf("DeleteInstance() error: %v", err)
	}
}

func TestDeleteInstance_NotFound(t *testing.T) {
	server := mockInstancesServer(t)
	defer server.Close()
	client := NewClient("rc_live_test", server.URL)

	err := client.DeleteInstance("not-found")
	if err == nil {
		t.Fatal("expected error for not-found delete, got nil")
	}
}

func TestGetSSHCertificate(t *testing.T) {
	server := mockInstancesServer(t)
	defer server.Close()
	client := NewClient("rc_live_test", server.URL)

	cert, err := client.GetSSHCertificate("inst-1", "ssh-ed25519 AAAA...")
	if err != nil {
		t.Fatalf("GetSSHCertificate() error: %v", err)
	}
	if cert.IP != "10.0.0.1" {
		t.Errorf("IP = %q, want %q", cert.IP, "10.0.0.1")
	}
	if cert.User != "root" {
		t.Errorf("user = %q, want %q", cert.User, "root")
	}
	if cert.Port != 22 {
		t.Errorf("port = %d, want 22", cert.Port)
	}
	if cert.Certificate == "" {
		t.Error("certificate is empty")
	}
}
