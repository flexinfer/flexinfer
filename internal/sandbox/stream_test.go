package sandbox

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestStreamWriter_Write(t *testing.T) {
	ch := make(chan ExecChunk, 10)
	sw := NewStreamWriter("stdout", ch)

	n, err := sw.Write([]byte("hello world"))
	require.NoError(t, err)
	assert.Equal(t, 11, n)

	chunk := <-ch
	assert.Equal(t, "stdout", chunk.Stream)
	assert.Equal(t, "hello world", chunk.Data)
	assert.WithinDuration(t, time.Now(), chunk.Timestamp, 2*time.Second)
}

func TestStreamWriter_WriteStderr(t *testing.T) {
	ch := make(chan ExecChunk, 10)
	sw := NewStreamWriter("stderr", ch)

	n, err := sw.Write([]byte("error msg"))
	require.NoError(t, err)
	assert.Equal(t, 9, n)

	chunk := <-ch
	assert.Equal(t, "stderr", chunk.Stream)
	assert.Equal(t, "error msg", chunk.Data)
}

func TestStreamWriter_EmptyWrite(t *testing.T) {
	ch := make(chan ExecChunk, 10)
	sw := NewStreamWriter("stdout", ch)

	n, err := sw.Write([]byte{})
	require.NoError(t, err)
	assert.Equal(t, 0, n)

	// Channel should be empty.
	select {
	case <-ch:
		t.Fatal("expected no chunk for empty write")
	default:
	}
}

func TestStreamWriter_Close(t *testing.T) {
	ch := make(chan ExecChunk, 10)
	sw := NewStreamWriter("stdout", ch)
	sw.Close()

	_, err := sw.Write([]byte("after close"))
	assert.ErrorIs(t, err, ErrStreamClosed)
}

func TestStreamWriter_MultipleChunks(t *testing.T) {
	ch := make(chan ExecChunk, 10)
	sw := NewStreamWriter("stdout", ch)

	for _, msg := range []string{"line1\n", "line2\n", "line3\n"} {
		n, err := sw.Write([]byte(msg))
		require.NoError(t, err)
		assert.Equal(t, len(msg), n)
	}

	for i, expected := range []string{"line1\n", "line2\n", "line3\n"} {
		chunk := <-ch
		assert.Equal(t, expected, chunk.Data, "chunk %d", i)
		assert.Equal(t, "stdout", chunk.Stream)
	}
}

func TestStreamWriter_DataIsCopied(t *testing.T) {
	ch := make(chan ExecChunk, 10)
	sw := NewStreamWriter("stdout", ch)

	buf := []byte("original")
	_, err := sw.Write(buf)
	require.NoError(t, err)

	// Mutate the original buffer after write.
	buf[0] = 'X'

	chunk := <-ch
	assert.Equal(t, "original", chunk.Data, "chunk data should not be affected by buffer mutation")
}
