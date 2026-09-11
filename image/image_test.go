package image

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"image"
	"image/color"
	"image/gif"
	"image/jpeg"
	"image/png"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/coder/acp-go-sdk"
	"github.com/stretchr/testify/require"

	acpcore "github.com/savid/acp-go-core"
	"github.com/savid/acp-go-core/wire"
)

func pngBytes(t *testing.T) []byte {
	t.Helper()

	var buf bytes.Buffer

	img := image.NewRGBA(image.Rect(0, 0, 2, 2))
	img.Set(0, 0, color.White)
	require.NoError(t, png.Encode(&buf, img))

	return buf.Bytes()
}

func jpegBytes(t *testing.T) []byte {
	t.Helper()

	var buf bytes.Buffer

	require.NoError(t, jpeg.Encode(&buf, image.NewRGBA(image.Rect(0, 0, 2, 2)), nil))

	return buf.Bytes()
}

func animatedGIF(t *testing.T) []byte {
	t.Helper()

	frame := image.NewPaletted(image.Rect(0, 0, 2, 2), color.Palette{color.Black, color.White})

	var buf bytes.Buffer

	require.NoError(t, gif.EncodeAll(&buf, &gif.GIF{Image: []*image.Paletted{frame, frame}, Delay: []int{1, 1}}))

	return buf.Bytes()
}

func imageBlock(data []byte, mime string) acp.ContentBlock {
	return acp.ContentBlock{Image: &acp.ContentBlockImage{Data: base64.StdEncoding.EncodeToString(data), MimeType: mime}}
}

func handoffBlock(uri, mime string, data []byte, size int64) acp.ContentBlock {
	sum := sha256.Sum256(data)

	return acp.ContentBlock{Image: &acp.ContentBlockImage{
		Data:     "",
		MimeType: mime,
		Uri:      &uri,
		Meta:     map[string]any{wire.HandoffKey: map[string]any{"version": 1, "digest": hex.EncodeToString(sum[:]), "sizeBytes": size}},
	}}
}

func validate(t *testing.T, options Options, blocks ...acp.ContentBlock) ([]Decoded, *InputError) {
	t.Helper()

	decoded, refusal, err := ValidatePrompt(context.Background(), blocks, options)
	require.NoError(t, err)

	return decoded, refusal
}

func TestMediaEnvelope(t *testing.T) {
	t.Parallel()

	envelope := MediaEnvelope(DefaultLimits(), Envelope{})
	require.Equal(t, DefaultLimitBytes, envelope["maxBytes"])
	require.Equal(t, DefaultLimitBytes, envelope["maxPromptBytes"])
	require.Equal(t, int64(0), envelope["maxDimension"])
	require.Equal(t, Formats, envelope["imageFormats"])
	require.Equal(t, []string{}, envelope["documentFormats"])

	disabled := MediaEnvelope(Limits{}, Envelope{NativeCeiling: 921_600, MaxDimension: 8000, DocumentFormats: []string{"application/pdf"}})
	require.Equal(t, int64(921_600), disabled["maxBytes"])
	require.Equal(t, int64(0), disabled["maxPromptBytes"])
	require.Equal(t, int64(8000), disabled["maxDimension"])

	require.Equal(t, FrameClamp, Limits{MaxInputBytesPerImage: FrameClamp + 1}.EffectiveInputPerImage(0))
	require.Equal(t, FrameClamp, Limits{}.EffectiveOutputPerImage())
	require.Error(t, Limits{MaxOutputBytesPerToolCall: -1}.Validate())
	require.NoError(t, DefaultLimits().Validate())
}

func TestEmbeddedGates(t *testing.T) {
	t.Parallel()

	options := Options{Limits: DefaultLimits()}
	uri := "file:///ignored.png"

	decoded, refusal := validate(t, options, imageBlock(pngBytes(t), MIMEPNG))
	require.Nil(t, refusal)
	require.Len(t, decoded, 1)
	require.Equal(t, MIMEPNG, decoded[0].MIME)
	require.Equal(t, 0, decoded[0].Index)

	withURI := imageBlock(pngBytes(t), MIMEPNG)
	withURI.Image.Uri = &uri
	decoded, refusal = validate(t, options, withURI)
	require.Nil(t, refusal, "data wins over uri")
	require.Len(t, decoded, 1)

	cases := []struct {
		name  string
		block acp.ContentBlock
		code  string
	}{
		{"missing data", acp.ContentBlock{Image: &acp.ContentBlockImage{MimeType: MIMEPNG}}, ErrorMissingData},
		{"invalid base64", acp.ContentBlock{Image: &acp.ContentBlockImage{Data: "!!!", MimeType: MIMEPNG}}, ErrorInvalidBase64},
		{"non-canonical mime", imageBlock(pngBytes(t), "IMAGE/PNG"), ErrorInvalidMediaType},
		{"unknown raster", imageBlock([]byte("not an image"), MIMEPNG), ErrorMediaTypeMismatch},
		{"declared vs sniffed", imageBlock(jpegBytes(t), MIMEPNG), ErrorMediaTypeMismatch},
		{"animated gif", imageBlock(animatedGIF(t), MIMEGIF), ErrorAnimatedUnsupported},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			_, refusal := validate(t, options, acp.ContentBlock{Text: &acp.ContentBlockText{Text: "hi"}}, tc.block)
			require.NotNil(t, refusal)
			require.Equal(t, tc.code, refusal.Code)
			require.Equal(t, FieldPromptImage, refusal.Field)
			require.Equal(t, 0, refusal.Index)
		})
	}
}

func TestByteLimits(t *testing.T) {
	t.Parallel()

	data := pngBytes(t)
	tight := Options{Limits: Limits{MaxInputBytesPerImage: int64(len(data)) - 1, MaxInputBytesPerPrompt: 0}}

	_, refusal := validate(t, tight, imageBlock(data, MIMEPNG))
	require.NotNil(t, refusal)
	require.Equal(t, ErrorTooLarge, refusal.Code)
	require.Equal(t, int64(len(data)), refusal.SizeBytes)
	require.Equal(t, int64(len(data))-1, refusal.MaxBytes)

	aggregate := Options{Limits: Limits{MaxInputBytesPerImage: FrameClamp, MaxInputBytesPerPrompt: int64(len(data)) + 1}}

	_, refusal = validate(t, aggregate, imageBlock(data, MIMEPNG), imageBlock(data, MIMEPNG))
	require.NotNil(t, refusal)
	require.Equal(t, ErrorTooLarge, refusal.Code)
	require.Equal(t, 1, refusal.Index, "the aggregate names the crossing block")
	require.Equal(t, int64(2*len(data)), refusal.SizeBytes)

	native := Options{Limits: DefaultLimits(), NativeCeiling: int64(len(data)) - 1}

	_, refusal = validate(t, native, imageBlock(data, MIMEPNG))
	require.NotNil(t, refusal)
	require.Equal(t, ErrorTooLarge, refusal.Code, "the effective per-image bound is the native ceiling")

	params := refusal.InvalidParams()
	require.Equal(t, -32602, params.Code)
	payload, ok := params.Data.(map[string]any)
	require.True(t, ok)
	require.Equal(t, ErrorTooLarge, payload["error"])
}

func TestHandoffForm(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	data := pngBytes(t)
	path := filepath.Join(root, "turn", "frame.png")
	require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o700))
	require.NoError(t, os.WriteFile(path, data, 0o600))

	options := Options{Limits: DefaultLimits(), HandoffRoot: root}
	uri := "file://" + path

	decoded, refusal := validate(t, options, handoffBlock(uri, MIMEPNG, data, int64(len(data))))
	require.Nil(t, refusal)
	require.Len(t, decoded, 1)
	require.Equal(t, data, decoded[0].Data, "handoff reaches the same bytes as the embedded form")

	_, refusal = validate(t, Options{Limits: DefaultLimits()}, handoffBlock(uri, MIMEPNG, data, int64(len(data))))
	require.Equal(t, ErrorInvalidHandoff, refusal.Code, "an unset root refuses the form")
	require.Equal(t, handoffCauseRootUnset, refusal.Message)

	bad := handoffBlock(uri, MIMEPNG, data, int64(len(data)))
	envelope, ok := bad.Image.Meta[wire.HandoffKey].(map[string]any)
	require.True(t, ok)
	envelope["extra"] = true
	_, refusal = validate(t, options, bad)
	require.Equal(t, ErrorInvalidHandoff, refusal.Code)

	outside := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(outside, "x.png"), data, 0o600))
	_, refusal = validate(t, options, handoffBlock("file://"+filepath.Join(outside, "x.png"), MIMEPNG, data, int64(len(data))))
	require.Equal(t, ErrorPathNotAllowed, refusal.Code)

	require.NoError(t, os.Symlink(filepath.Join(outside, "x.png"), filepath.Join(root, "escape.png")))
	_, refusal = validate(t, options, handoffBlock("file://"+filepath.Join(root, "escape.png"), MIMEPNG, data, int64(len(data))))
	require.Equal(t, ErrorPathNotAllowed, refusal.Code, "an absolute symlink is refused even when its target is readable")

	_, refusal = validate(t, options, handoffBlock("file://"+filepath.Join(root, "gone.png"), MIMEPNG, data, int64(len(data))))
	require.Equal(t, ErrorMissingFile, refusal.Code)

	_, refusal = validate(t, options, handoffBlock(uri, MIMEPNG, []byte("other"), int64(len(data))))
	require.Equal(t, ErrorDigestMismatch, refusal.Code)

	_, refusal = validate(t, options, handoffBlock(uri, MIMEPNG, data, int64(len(data))-1))
	require.Equal(t, ErrorDigestMismatch, refusal.Code, "a file larger than declared forwards nothing")

	_, refusal = validate(t, options, handoffBlock("file://"+filepath.Join(root, "absent.png"), "image/bmp", data, int64(len(data))))
	require.Equal(t, ErrorInvalidMediaType, refusal.Code, "a declared MIME outside the allowlist opens nothing")

	_, refusal = validate(t, Options{Limits: Limits{MaxInputBytesPerImage: 10}, HandoffRoot: root},
		handoffBlock("file://"+filepath.Join(root, "absent.png"), MIMEPNG, data, int64(len(data))))
	require.Equal(t, ErrorTooLarge, refusal.Code, "a declared size above the bound opens nothing")

	_, refusal = validate(t, options, acp.ContentBlock{Image: &acp.ContentBlockImage{MimeType: MIMEPNG}})
	require.Equal(t, ErrorMissingData, refusal.Code, "empty data with no handoff intent stays missing_data")

	blocks := make([]acp.ContentBlock, 0, MaxHandoffBlocksPerPrompt+1)
	for range MaxHandoffBlocksPerPrompt + 1 {
		blocks = append(blocks, handoffBlock(uri, MIMEPNG, data, int64(len(data))))
	}

	_, refusal = validate(t, Options{Limits: Limits{MaxInputBytesPerImage: FrameClamp}, HandoffRoot: root}, blocks...)
	require.Equal(t, ErrorTooLarge, refusal.Code)
	require.Equal(t, MaxHandoffBlocksPerPrompt, refusal.Index)
	require.Equal(t, int64(MaxHandoffBlocksPerPrompt+1), refusal.SizeBytes)
	require.Equal(t, int64(MaxHandoffBlocksPerPrompt), refusal.MaxBytes)
}

func TestResourceBlocks(t *testing.T) {
	t.Parallel()

	data := pngBytes(t)
	pdf := "application/pdf"
	blob := acp.ContentBlock{Resource: &acp.ContentBlockResource{Resource: acp.EmbeddedResourceResource{
		BlobResourceContents: &acp.BlobResourceContents{Blob: base64.StdEncoding.EncodeToString([]byte("%PDF-")), MimeType: &pdf},
	}}}
	text := acp.ContentBlock{Resource: &acp.ContentBlockResource{Resource: acp.EmbeddedResourceResource{
		TextResourceContents: &acp.TextResourceContents{Text: strings.Repeat("t", 10)},
	}}}

	decoded, refusal := validate(t, Options{Limits: DefaultLimits()}, blob, text, imageBlock(data, MIMEPNG))
	require.Nil(t, refusal)
	require.Len(t, decoded, 1)
	require.Equal(t, 1, decoded[0].Index, "a gated blob consumes an index and a text resource does not")

	refuse := Options{Limits: DefaultLimits(), Blobs: func(string) BlobDisposition { return BlobRefuse }}
	_, refusal = validate(t, refuse, blob)
	require.Equal(t, "unsupported", refusal.Code)
	require.Equal(t, FieldPromptResource, refusal.Field)

	_, refusal = validate(t, Options{Limits: Limits{MaxInputBytesPerImage: FrameClamp, MaxInputBytesPerPrompt: 12}}, text, blob)
	require.Equal(t, ErrorTooLarge, refusal.Code, "text bytes count toward the aggregate")
	require.Equal(t, FieldPromptResource, refusal.Field)

	imageMIME := MIMEPNG
	imageBlob := acp.ContentBlock{Resource: &acp.ContentBlockResource{Resource: acp.EmbeddedResourceResource{
		BlobResourceContents: &acp.BlobResourceContents{Blob: base64.StdEncoding.EncodeToString(data), MimeType: &imageMIME},
	}}}
	decoded, refusal = validate(t, Options{Limits: DefaultLimits()}, imageBlob)
	require.Nil(t, refusal)
	require.Equal(t, FieldPromptResource, decoded[0].Field)
}

func TestOutput(t *testing.T) {
	t.Parallel()

	data := pngBytes(t)

	got, mime, size, verdict := DecodeInline(base64.StdEncoding.EncodeToString(data), FrameClamp)
	require.Nil(t, verdict)
	require.Equal(t, data, got)
	require.Equal(t, MIMEPNG, mime)
	require.Equal(t, int64(len(data)), size)

	_, _, size, verdict = DecodeInline("!!!", FrameClamp)
	require.Zero(t, size)
	require.Equal(t, ReasonInvalidBase64, verdict.Reason)

	_, _, size, verdict = DecodeInline(base64.StdEncoding.EncodeToString(data), int64(len(data))-1)
	require.Equal(t, int64(len(data)), size)
	require.Equal(t, ReasonTooLarge, verdict.Reason)

	_, _, size, verdict = DecodeInline(base64.StdEncoding.EncodeToString([]byte("plain")), FrameClamp)
	require.Zero(t, size)
	require.Equal(t, ReasonNotRaster, verdict.Reason)

	guidance, recoverable := verdict.Guidance()
	require.True(t, recoverable)
	require.Equal(t, GuidanceNotRaster, guidance)

	_, fatal := (&OutputError{Reason: ReasonStorageFailed}).Guidance()
	require.False(t, fatal)
	require.Equal(t, OutputStage, (&OutputError{Reason: ReasonStorageFailed, Message: "x"}).TurnFailure().Stage)

	root := t.TempDir()
	path := filepath.Join(root, "out.png")
	require.NoError(t, os.WriteFile(path, data, 0o600))

	got, mime, verdict = ReadFile(path, []string{root}, FrameClamp)
	require.Nil(t, verdict)
	require.Equal(t, data, got)
	require.Equal(t, MIMEPNG, mime)

	_, _, verdict = ReadFile(path, []string{t.TempDir()}, FrameClamp)
	require.Equal(t, ReasonPathNotAllowed, verdict.Reason)

	_, _, verdict = ReadFile(filepath.Join(root, "missing.png"), []string{root}, FrameClamp)
	require.Equal(t, ReasonMissingFile, verdict.Reason)

	_, _, verdict = ReadFile(root, []string{root}, FrameClamp)
	require.Equal(t, ReasonPathNotAllowed, verdict.Reason)

	bmp, ok := SniffMIME([]byte("BM\x00\x00\x00\x00\x00\x00\x00\x00\x00\x00\x00\x00"))
	require.True(t, ok)
	require.Equal(t, "image/bmp", bmp)
}

func TestArtifactStore(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	store := acpcore.NewInMemorySessionStore()
	artifacts := NewArtifactStore(store, "s")
	now := time.Now()
	artifacts.now = func() time.Time { return now }

	data := pngBytes(t)

	subpath, err := artifacts.Store(ctx, "native-1", data, MIMEPNG)
	require.NoError(t, err)
	require.True(t, strings.HasPrefix(subpath, ArtifactPrefix))

	again, err := artifacts.Store(ctx, "native-1", data, MIMEPNG)
	require.NoError(t, err)
	require.Equal(t, subpath, again)

	loaded, err := artifacts.Load(ctx, subpath)
	require.NoError(t, err)
	require.Equal(t, MIMEPNG, loaded.MimeType)
	require.Equal(t, base64.StdEncoding.EncodeToString(data), loaded.Data)

	_, err = artifacts.Load(ctx, "other/x")
	require.Error(t, err)

	artifacts.now = func() time.Time { return now.Add(ArtifactTTL) }

	_, err = artifacts.Load(ctx, subpath)
	require.Error(t, err, "an expired artifact fails replay")

	subkeys, err := store.ListSubkeys(ctx, acpcore.SessionKey{SessionID: "s"})
	require.NoError(t, err)
	require.Empty(t, subkeys, "an expired artifact is deleted on access")
}
