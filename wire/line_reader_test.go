package wire

import (
	"io"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestLineReaderSplitsOnlyOnLF(t *testing.T) {
	t.Parallel()

	reader := NewLineReader(strings.NewReader("{\"a\":\"x y\"}\r\n\n"+strings.Repeat("z", 200<<10)+"\nlast\n"), 0)

	record, err := reader.Next()
	require.NoError(t, err)
	require.Equal(t, "{\"a\":\"x y\"}", string(record))

	record, err = reader.Next()
	require.NoError(t, err)
	require.Empty(t, record)

	record, err = reader.Next()
	require.NoError(t, err)
	require.Len(t, record, 200<<10, "a record longer than the read buffer is returned whole")

	record, err = reader.Next()
	require.NoError(t, err)
	require.Equal(t, "last", string(record))

	_, err = reader.Next()
	require.ErrorIs(t, err, io.EOF)
}

func TestLineReaderRefusesOversizedAndUnterminatedRecords(t *testing.T) {
	t.Parallel()

	reader := NewLineReader(strings.NewReader("12345\n123456\n"), 5)

	record, err := reader.Next()
	require.NoError(t, err)
	require.Equal(t, "12345", string(record))

	_, err = reader.Next()
	require.ErrorIs(t, err, ErrLineTooLong)

	_, err = NewLineReader(strings.NewReader("whole\npartial"), 0).Next()
	require.NoError(t, err)

	partial := NewLineReader(strings.NewReader("partial"), 0)
	_, err = partial.Next()
	require.ErrorIs(t, err, io.ErrUnexpectedEOF)
}
