package schema

import "testing"

func BenchmarkContentHashString(b *testing.B) {
	payload := make([]byte, 256*1024)
	for i := range payload {
		payload[i] = byte('a' + (i % 26))
	}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = ContentHash(string(payload))
	}
}

func BenchmarkContentHashBytes(b *testing.B) {
	payload := make([]byte, 256*1024)
	for i := range payload {
		payload[i] = byte('a' + (i % 26))
	}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = ContentHashBytes(payload)
	}
}
