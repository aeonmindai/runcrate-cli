package commands

import "testing"

func TestParseGPUFlag(t *testing.T) {
	tests := []struct {
		name            string
		gpuFlag         string
		gpuCountFlag    int
		gpuCountChanged bool
		wantType        string
		wantCount       int
		wantErr         bool
	}{
		{
			name:      "colon syntax H100:2",
			gpuFlag:   "H100:2",
			wantType:  "H100",
			wantCount: 2,
		},
		{
			name:      "colon syntax A100:4",
			gpuFlag:   "A100:4",
			wantType:  "A100",
			wantCount: 4,
		},
		{
			name:      "colon syntax single GPU",
			gpuFlag:   "L4:1",
			wantType:  "L4",
			wantCount: 1,
		},
		{
			name:         "plain type with default count",
			gpuFlag:      "H100",
			gpuCountFlag: 1,
			wantType:     "H100",
			wantCount:    1,
		},
		{
			name:            "plain type with explicit gpu-count",
			gpuFlag:         "H100",
			gpuCountFlag:    4,
			gpuCountChanged: true,
			wantType:        "H100",
			wantCount:       4,
		},
		{
			name:            "colon syntax conflicts with gpu-count",
			gpuFlag:         "H100:2",
			gpuCountFlag:    4,
			gpuCountChanged: true,
			wantErr:         true,
		},
		{
			name:    "colon with invalid count",
			gpuFlag: "H100:abc",
			wantErr: true,
		},
		{
			name:    "colon with zero count",
			gpuFlag: "H100:0",
			wantErr: true,
		},
		{
			name:    "colon with negative count",
			gpuFlag: "H100:-1",
			wantErr: true,
		},
		{
			name:      "empty gpu flag",
			gpuFlag:   "",
			wantType:  "",
			wantCount: 1,
			gpuCountFlag: 1,
		},
		{
			name:         "empty gpu flag with types default",
			gpuFlag:      "",
			gpuCountFlag: 0,
			wantType:     "",
			wantCount:    0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gotType, gotCount, err := parseGPUFlag(tt.gpuFlag, tt.gpuCountFlag, tt.gpuCountChanged)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("expected error, got nil")
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if gotType != tt.wantType {
				t.Errorf("type = %q, want %q", gotType, tt.wantType)
			}
			if gotCount != tt.wantCount {
				t.Errorf("count = %d, want %d", gotCount, tt.wantCount)
			}
		})
	}
}
