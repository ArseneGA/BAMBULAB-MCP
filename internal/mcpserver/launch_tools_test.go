package mcpserver

import (
	"bytes"
	"encoding/json"
	"image"
	"image/color"
	"image/jpeg"
	"strings"
	"testing"
)

func TestShrinkJPEGKeepsPhotosUnderTheResultLimit(t *testing.T) {
	img := image.NewRGBA(image.Rect(0, 0, 1280, 720))
	for y := 0; y < 720; y++ {
		for x := 0; x < 1280; x++ {
			img.Set(x, y, color.RGBA{uint8(x * 7), uint8(y * 3), uint8(x ^ y), 255})
		}
	}
	var raw bytes.Buffer
	if err := jpeg.Encode(&raw, img, &jpeg.Options{Quality: 95}); err != nil {
		t.Fatal(err)
	}
	small, err := shrinkJPEG(raw.Bytes(), 480, 70)
	if err != nil {
		t.Fatal(err)
	}
	cfg, err := jpeg.DecodeConfig(bytes.NewReader(small))
	if err != nil || cfg.Width > 480 || cfg.Width < 200 {
		t.Fatalf("dimensions : %+v %v", cfg, err)
	}
	if b64 := len(small) * 4 / 3; b64 > 100_000 {
		t.Fatalf("photo trop lourde pour un résultat d'outil : %d caractères", b64)
	}
	if _, err := shrinkJPEG([]byte("pas une image"), 480, 70); err == nil {
		t.Fatal("octets quelconques acceptés")
	}
}

func TestMaskSecretsRedactsEveryFormOfTheCode(t *testing.T) {
	for _, args := range []string{`{"code":907351}`, `{"code":"907351"}`, `{"code":" 907351 "}`, `{"code":["907351"]}`} {
		secrets := codeSecrets(json.RawMessage(args))
		msg := maskSecrets(`validating /properties/code: type: 907351 has type integer; got "907351"; ["907351"]`, secrets)
		if strings.Contains(msg, "907351") {
			t.Errorf("%s : %s", args, msg)
		}
	}
	if len(codeSecrets(json.RawMessage(`{"autre":"907351"}`))) != 0 || len(codeSecrets(json.RawMessage(`pas du json`))) != 0 {
		t.Fatal("secrets inattendus")
	}
}
