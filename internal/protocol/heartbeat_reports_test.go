package protocol

import (
	"crypto/ed25519"
	"encoding/base64"
	"encoding/binary"
	"errors"
	"math"
	"strings"
	"testing"

	"google.golang.org/protobuf/encoding/protowire"
	"google.golang.org/protobuf/proto"

	agentv1 "moonglass.dev/moonglass/internal/protocol/agentv1"
)

// wireUint32 returns n as the four big-endian bytes an SSH string length uses.
func wireUint32(n uint32) []byte {
	b := make([]byte, 4)
	binary.BigEndian.PutUint32(b, n)
	return b
}

// deployKeyLine returns an OpenSSH public key line carrying pub in the Ed25519
// wire format: the key type, its 32-byte public key, then the comment.
func deployKeyLine(t testing.TB, pub []byte) string {
	t.Helper()
	if len(pub) != 32 {
		t.Fatalf("deploy key length: expected a 32-byte Ed25519 public key, got %d", len(pub))
	}
	blob := append(wireUint32(11), "ssh-ed25519"...)
	blob = append(blob, wireUint32(32)...)
	blob = append(blob, pub...)
	return "ssh-ed25519 " + base64.StdEncoding.EncodeToString(blob) + " moonglass-deploy"
}

// deployKeyOfB64Len returns a line whose base64 part is n characters long.
func deployKeyOfB64Len(n int) string {
	return "ssh-ed25519 " + base64.StdEncoding.EncodeToString(make([]byte, n/4*3)) + " moonglass-deploy"
}

// validDiskReport returns a disk report that passes every validator.
func validDiskReport() *agentv1.DiskReport {
	return &agentv1.DiskReport{
		DataRootFreeBytes:  50,
		DataRootTotalBytes: 100,
		AgentDirFreeBytes:  25,
		AgentDirTotalBytes: 100,
		Level:              agentv1.DiskLevel_OK,
	}
}

// heartbeatRejectedNaming expects Open to reject msg with ErrMalformed and an
// error that names field.
func heartbeatRejectedNaming(t *testing.T, msg *agentv1.Heartbeat, field string) {
	t.Helper()
	env, err := Wrap(agentv1.MessageType_HEARTBEAT, "req-1", msg)
	if err != nil {
		t.Fatalf("Wrap(HEARTBEAT) error = %v", err)
	}
	err = Open(env, &agentv1.Heartbeat{})
	if !errors.Is(err, ErrMalformed) {
		t.Fatalf("Open(%v): expected ErrMalformed, got %v", msg, err)
	}
	if !strings.Contains(err.Error(), field) {
		t.Fatalf("Open(%v) error = %q, expected it to name field %q", msg, err, field)
	}
}

func TestHeartbeatReportsRoundTrip(t *testing.T) {
	t.Parallel()
	assertNoShellStringFields(t, (&agentv1.Heartbeat{}).ProtoReflect().Descriptor())
	assertNoShellStringFields(t, (&agentv1.DiskReport{}).ProtoReflect().Descriptor())

	seed := make([]byte, ed25519.SeedSize)
	pub := ed25519.NewKeyFromSeed(seed).Public().(ed25519.PublicKey)
	key := deployKeyLine(t, pub)
	longest := deployKeyOfB64Len(224)

	tests := []struct {
		check func(*testing.T, *agentv1.Heartbeat)
		msg   *agentv1.Heartbeat
		name  string
	}{
		{
			name: "no reports",
			msg:  &agentv1.Heartbeat{AgentVersion: "dev"},
			check: func(t *testing.T, got *agentv1.Heartbeat) {
				t.Helper()
				if got.GetDisk() != nil {
					t.Fatalf("disk: expected nil after round trip, got %v", got.GetDisk())
				}
			},
		},
		{
			name: "disk OK",
			msg:  &agentv1.Heartbeat{AgentVersion: "dev", Disk: validDiskReport()},
		},
		{
			name: "disk LOW",
			msg: &agentv1.Heartbeat{AgentVersion: "dev", Disk: &agentv1.DiskReport{
				DataRootFreeBytes:  10,
				DataRootTotalBytes: 100,
				AgentDirFreeBytes:  10,
				AgentDirTotalBytes: 100,
				Level:              agentv1.DiskLevel_LOW,
			}},
		},
		{
			name: "disk CRITICAL",
			msg: &agentv1.Heartbeat{AgentVersion: "dev", Disk: &agentv1.DiskReport{
				DataRootFreeBytes:  1,
				DataRootTotalBytes: 100,
				AgentDirFreeBytes:  1,
				AgentDirTotalBytes: 100,
				Level:              agentv1.DiskLevel_CRITICAL,
			}},
		},
		{
			name: "free equal to total",
			msg: &agentv1.Heartbeat{AgentVersion: "dev", Disk: &agentv1.DiskReport{
				DataRootFreeBytes:  100,
				DataRootTotalBytes: 100,
				AgentDirFreeBytes:  100,
				AgentDirTotalBytes: 100,
				Level:              agentv1.DiskLevel_OK,
			}},
		},
		{
			name: "every byte count zero",
			msg: &agentv1.Heartbeat{AgentVersion: "dev", Disk: &agentv1.DiskReport{
				Level: agentv1.DiskLevel_OK,
			}},
		},
		{
			name: "max uint64 free and total",
			msg: &agentv1.Heartbeat{AgentVersion: "dev", Disk: &agentv1.DiskReport{
				DataRootFreeBytes:  math.MaxUint64,
				DataRootTotalBytes: math.MaxUint64,
				AgentDirFreeBytes:  math.MaxUint64,
				AgentDirTotalBytes: math.MaxUint64,
				Level:              agentv1.DiskLevel_OK,
			}},
		},
		{
			name: "deploy key",
			msg:  &agentv1.Heartbeat{AgentVersion: "dev", DeployKey: key},
		},
		{
			name: "253 byte deploy key",
			msg:  &agentv1.Heartbeat{AgentVersion: "dev", DeployKey: longest},
			check: func(t *testing.T, got *agentv1.Heartbeat) {
				t.Helper()
				if n := len(got.GetDeployKey()); n != 253 {
					t.Fatalf("deploy_key length: expected 253, got %d", n)
				}
			},
		},
		{
			name: "disk and deploy key together",
			msg:  &agentv1.Heartbeat{AgentVersion: "dev", Disk: validDiskReport(), DeployKey: key},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			var got agentv1.Heartbeat
			roundTrip(t, agentv1.MessageType_HEARTBEAT, tt.msg, &got)
			if tt.check != nil {
				tt.check(t, &got)
			}
		})
	}
}

func TestHeartbeatRejectsBadDiskReport(t *testing.T) {
	t.Parallel()

	tests := []struct {
		disk  *agentv1.DiskReport
		name  string
		field string
	}{
		{
			name: "data root free one above total",
			disk: &agentv1.DiskReport{
				DataRootFreeBytes:  101,
				DataRootTotalBytes: 100,
				AgentDirFreeBytes:  1,
				AgentDirTotalBytes: 100,
				Level:              agentv1.DiskLevel_OK,
			},
			field: "disk.data_root_free_bytes",
		},
		{
			name: "agent dir free one above total",
			disk: &agentv1.DiskReport{
				DataRootFreeBytes:  1,
				DataRootTotalBytes: 100,
				AgentDirFreeBytes:  101,
				AgentDirTotalBytes: 100,
				Level:              agentv1.DiskLevel_OK,
			},
			field: "disk.agent_dir_free_bytes",
		},
		{
			name: "data root free with zero total",
			disk: &agentv1.DiskReport{
				DataRootFreeBytes:  1,
				DataRootTotalBytes: 0,
				AgentDirFreeBytes:  0,
				AgentDirTotalBytes: 0,
				Level:              agentv1.DiskLevel_OK,
			},
			field: "disk.data_root_free_bytes",
		},
		{
			name: "agent dir free with zero total",
			disk: &agentv1.DiskReport{
				DataRootFreeBytes:  0,
				DataRootTotalBytes: 0,
				AgentDirFreeBytes:  1,
				AgentDirTotalBytes: 0,
				Level:              agentv1.DiskLevel_OK,
			},
			field: "disk.agent_dir_free_bytes",
		},
		{
			name: "level unspecified",
			disk: &agentv1.DiskReport{
				DataRootFreeBytes:  1,
				DataRootTotalBytes: 100,
				AgentDirFreeBytes:  1,
				AgentDirTotalBytes: 100,
				Level:              agentv1.DiskLevel_DISK_LEVEL_UNSPECIFIED,
			},
			field: "disk.level",
		},
		{
			name: "level 4",
			disk: &agentv1.DiskReport{
				DataRootFreeBytes:  1,
				DataRootTotalBytes: 100,
				AgentDirFreeBytes:  1,
				AgentDirTotalBytes: 100,
				Level:              agentv1.DiskLevel(4),
			},
			field: "disk.level",
		},
		{
			name: "level max int32",
			disk: &agentv1.DiskReport{
				DataRootFreeBytes:  1,
				DataRootTotalBytes: 100,
				AgentDirFreeBytes:  1,
				AgentDirTotalBytes: 100,
				Level:              agentv1.DiskLevel(math.MaxInt32),
			},
			field: "disk.level",
		},
		{
			name:  "present but empty disk report",
			disk:  &agentv1.DiskReport{},
			field: "disk.level",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			m := &agentv1.Heartbeat{AgentVersion: "dev", Disk: tt.disk}
			heartbeatRejectedNaming(t, m, tt.field)
		})
	}
}

func TestHeartbeatRejectsBadDeployKey(t *testing.T) {
	t.Parallel()

	validB64 := base64.StdEncoding.EncodeToString(make([]byte, 32))
	line := func(b64 string) string {
		return "ssh-ed25519 " + b64 + " moonglass-deploy"
	}

	tests := []struct {
		name string
		key  string
	}{
		{"257 bytes", deployKeyOfB64Len(228)},
		{"ssh-rsa prefix", "ssh-rsa " + validB64 + " moonglass-deploy"},
		{"no comment", "ssh-ed25519 " + validB64},
		{"other comment", "ssh-ed25519 " + validB64 + " other"},
		{"two spaces after type", "ssh-ed25519  " + validB64 + " moonglass-deploy"},
		{"two spaces before comment", "ssh-ed25519 " + validB64 + "  moonglass-deploy"},
		{"trailing newline", line(validB64) + "\n"},
		{"leading space", " " + line(validB64)},
		{"empty base64", "ssh-ed25519  moonglass-deploy"},
		{"dash in base64", line("AAAA-AAA")},
		{"underscore in base64", line("AAAA_AAA")},
		{"unpadded base64", line("AAAAAAA")},
		{"non-zero padding bits", line("AB==")},
		{"NUL in base64", line("AAAA\x00AAA")},
		{"newline in base64", line("AAAA\nAAA")},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			m := &agentv1.Heartbeat{AgentVersion: "dev", DeployKey: tt.key}
			heartbeatRejectedNaming(t, m, "deploy_key")
		})
	}
}

func TestHeartbeatReportsProtocolAbuse(t *testing.T) {
	t.Parallel()
	assertNoShellStringFields(t, (&agentv1.Heartbeat{}).ProtoReflect().Descriptor())
	assertNoShellStringFields(t, (&agentv1.DiskReport{}).ProtoReflect().Descriptor())

	t.Run("unknown top-level field", func(t *testing.T) {
		t.Parallel()
		payload, err := proto.Marshal(&agentv1.Heartbeat{AgentVersion: "dev"})
		if err != nil {
			t.Fatalf("proto.Marshal(Heartbeat) error = %v", err)
		}
		payload = protowire.AppendTag(payload, 99, protowire.VarintType)
		payload = protowire.AppendVarint(payload, 1)
		env := &agentv1.Envelope{
			Version:   Version,
			Type:      agentv1.MessageType_HEARTBEAT,
			RequestId: "r",
			Payload:   payload,
		}
		if err := Open(env, &agentv1.Heartbeat{}); !errors.Is(err, ErrMalformed) {
			t.Fatalf("Open with unknown Heartbeat field: expected ErrMalformed, got %v", err)
		}
	})

	t.Run("unknown nested DiskReport field", func(t *testing.T) {
		t.Parallel()
		disk, err := proto.Marshal(validDiskReport())
		if err != nil {
			t.Fatalf("proto.Marshal(DiskReport) error = %v", err)
		}
		disk = protowire.AppendTag(disk, 99, protowire.VarintType)
		disk = protowire.AppendVarint(disk, 1)
		payload, err := proto.Marshal(&agentv1.Heartbeat{AgentVersion: "dev"})
		if err != nil {
			t.Fatalf("proto.Marshal(Heartbeat) error = %v", err)
		}
		// disk is field 2 of Heartbeat.
		payload = protowire.AppendTag(payload, 2, protowire.BytesType)
		payload = protowire.AppendBytes(payload, disk)
		env := &agentv1.Envelope{
			Version:   Version,
			Type:      agentv1.MessageType_HEARTBEAT,
			RequestId: "r",
			Payload:   payload,
		}
		if err := Open(env, &agentv1.Heartbeat{}); !errors.Is(err, ErrMalformed) {
			t.Fatalf("Open with unknown DiskReport field: expected ErrMalformed, got %v", err)
		}
	})

	t.Run("type mismatches", func(t *testing.T) {
		t.Parallel()
		heartbeatEnv, err := Wrap(agentv1.MessageType_HEARTBEAT, "r", &agentv1.Heartbeat{AgentVersion: "dev"})
		if err != nil {
			t.Fatalf("Wrap(HEARTBEAT) error = %v", err)
		}
		if err := Open(heartbeatEnv, &agentv1.Build{}); !errors.Is(err, ErrTypeMismatch) {
			t.Fatalf("Open(HEARTBEAT as Build): expected ErrTypeMismatch, got %v", err)
		}

		buildEnv, err := Wrap(agentv1.MessageType_BUILD, "r", validBuild())
		if err != nil {
			t.Fatalf("Wrap(BUILD) error = %v", err)
		}
		if err := Open(buildEnv, &agentv1.Heartbeat{}); !errors.Is(err, ErrTypeMismatch) {
			t.Fatalf("Open(BUILD as Heartbeat): expected ErrTypeMismatch, got %v", err)
		}
	})
}

func FuzzOpenHeartbeat(f *testing.F) {
	minimal, err := proto.Marshal(&agentv1.Heartbeat{AgentVersion: "dev"})
	if err != nil {
		f.Fatalf("proto.Marshal(minimal Heartbeat) error = %v", err)
	}
	f.Add(minimal)

	pub := ed25519.NewKeyFromSeed(make([]byte, ed25519.SeedSize)).Public().(ed25519.PublicKey)
	full, err := proto.Marshal(&agentv1.Heartbeat{
		AgentVersion: "dev",
		Disk:         validDiskReport(),
		DeployKey:    deployKeyLine(f, pub),
	})
	if err != nil {
		f.Fatalf("proto.Marshal(full Heartbeat) error = %v", err)
	}
	f.Add(full)
	f.Add([]byte(nil))

	disk, err := proto.Marshal(validDiskReport())
	if err != nil {
		f.Fatalf("proto.Marshal(DiskReport) error = %v", err)
	}
	disk = protowire.AppendTag(disk, 99, protowire.VarintType)
	disk = protowire.AppendVarint(disk, 1)
	nested, err := proto.Marshal(&agentv1.Heartbeat{AgentVersion: "dev"})
	if err != nil {
		f.Fatalf("proto.Marshal(Heartbeat) error = %v", err)
	}
	// disk is field 2 of Heartbeat.
	nested = protowire.AppendTag(nested, 2, protowire.BytesType)
	nested = protowire.AppendBytes(nested, disk)
	f.Add(nested)

	f.Fuzz(func(t *testing.T, in []byte) {
		env := &agentv1.Envelope{
			Version:   Version,
			Type:      agentv1.MessageType_HEARTBEAT,
			RequestId: "fuzz",
			Payload:   in,
		}
		var first agentv1.Heartbeat
		if err := Open(env, &first); err != nil {
			if !errors.Is(err, ErrMalformed) {
				t.Fatalf("Open(HEARTBEAT, %x) error = %v, expected nil or ErrMalformed", in, err)
			}
			return
		}
		if err := validateHeartbeat(&first); err != nil {
			t.Fatalf("Open accepted HEARTBEAT payload %x but validateHeartbeat failed: %v", in, err)
		}
		if disk := first.GetDisk(); disk != nil {
			if disk.GetDataRootFreeBytes() > disk.GetDataRootTotalBytes() {
				t.Fatalf("accepted HEARTBEAT payload %x has data_root free %d above total %d",
					in, disk.GetDataRootFreeBytes(), disk.GetDataRootTotalBytes())
			}
			if disk.GetAgentDirFreeBytes() > disk.GetAgentDirTotalBytes() {
				t.Fatalf("accepted HEARTBEAT payload %x has agent_dir free %d above total %d",
					in, disk.GetAgentDirFreeBytes(), disk.GetAgentDirTotalBytes())
			}
			level := disk.GetLevel()
			if level != agentv1.DiskLevel_OK && level != agentv1.DiskLevel_LOW && level != agentv1.DiskLevel_CRITICAL {
				t.Fatalf("accepted HEARTBEAT payload %x has unknown disk level %d", in, level)
			}
		}
		wrapped, err := Wrap(agentv1.MessageType_HEARTBEAT, "fuzz", &first)
		if err != nil {
			t.Fatalf("Wrap(accepted Heartbeat %v) error = %v", &first, err)
		}
		var again agentv1.Heartbeat
		if err := Open(wrapped, &again); err != nil {
			t.Fatalf("Open after re-wrap error = %v", err)
		}
		if !proto.Equal(&first, &again) {
			t.Fatalf("round-trip mismatch: first %v, again %v", &first, &again)
		}
	})
}
