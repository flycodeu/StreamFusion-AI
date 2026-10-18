package deploy

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"hash/crc32"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/google/uuid"
)

func jarFixture(t *testing.T, manifest string, classVersion uint16) (string, string, int64) {
	t.Helper()
	var b bytes.Buffer
	w := zip.NewWriter(&b)
	for _, item := range []struct {
		name string
		data []byte
	}{
		{"META-INF/MANIFEST.MF", []byte(manifest)},
		{"demo/Main.class", []byte{0xca, 0xfe, 0xba, 0xbe, 0, 0, 0, 0}},
	} {
		if strings.HasSuffix(item.name, ".class") {
			binary.BigEndian.PutUint16(item.data[6:], classVersion)
		}
		entry, err := w.Create(item.name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err = entry.Write(item.data); err != nil {
			t.Fatal(err)
		}
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	filename := filepath.Join(t.TempDir(), "app.jar")
	if err := os.WriteFile(filename, b.Bytes(), 0600); err != nil {
		t.Fatal(err)
	}
	hash := sha256.Sum256(b.Bytes())
	return filename, hex.EncodeToString(hash[:]), int64(b.Len())
}

func tarEntries(t *testing.T, filename string) map[string][]byte {
	t.Helper()
	f, err := os.Open(filename)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	r := tar.NewReader(f)
	entries := map[string][]byte{}
	for {
		h, err := r.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		data, err := io.ReadAll(r)
		if err != nil {
			t.Fatal(err)
		}
		entries[h.Name] = data
	}
	return entries
}

func untagRuntimeArchive(t *testing.T, filename string) (string, int64) {
	t.Helper()
	entries := tarEntries(t, filename)
	var manifest []map[string]any
	if err := json.Unmarshal(entries["manifest.json"], &manifest); err != nil {
		t.Fatal(err)
	}
	manifest[0]["RepoTags"] = nil
	entries["manifest.json"], _ = json.Marshal(manifest)
	var b bytes.Buffer
	w := tar.NewWriter(&b)
	for name, data := range entries {
		if err := w.WriteHeader(&tar.Header{Name: name, Typeflag: tar.TypeReg, Mode: 0600, Size: int64(len(data))}); err != nil {
			t.Fatal(err)
		}
		if _, err := w.Write(data); err != nil {
			t.Fatal(err)
		}
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filename, b.Bytes(), 0600); err != nil {
		t.Fatal(err)
	}
	hash := sha256.Sum256(b.Bytes())
	return hex.EncodeToString(hash[:]), int64(b.Len())
}

func TestJavaJarPassivePackagingFreezesRuntimeAndLayer(t *testing.T) {
	base, _, _, baseImage := testArchive(t, "amd64", nil)
	baseHash, baseSize := untagRuntimeArchive(t, base)
	if _, err := ValidateArchive(base, baseHash, baseSize); err == nil {
		t.Fatal("untagged runtime accepted as user application")
	}
	if image, err := ValidateRuntimeArchive(base, baseHash, baseSize); err != nil || image != baseImage {
		t.Fatal(image, err)
	}
	jar, jarHash, jarSize := jarFixture(t, "Manifest-Version: 1.0\r\nMain-Class: demo.Main\r\n\r\n", 65)
	out := filepath.Join(t.TempDir(), "packaged.tar")
	env := RuntimeEnvironment{InstallationID: uuid.NewString(), TargetID: uuid.NewString(), PluginVersion: "21-jre", ImageID: baseImage, ImageDigest: "sha256:" + strings.Repeat("a", 64), RecipeRevision: "java-runtime-v1"}
	job := uuid.NewString()
	image, sha, size, err := PackageJavaJar(base, jar, out, baseImage, jarHash, jarSize, job, env)
	if err != nil {
		t.Fatal(err)
	}
	if actual, err := ValidateArchive(out, sha, size); err != nil || actual != image || image == baseImage {
		t.Fatal(actual, image, err)
	}
	entries := tarEntries(t, out)
	var manifest []struct {
		Config           string
		RepoTags, Layers []string
	}
	if err := json.Unmarshal(entries["manifest.json"], &manifest); err != nil {
		t.Fatal(err)
	}
	if len(manifest) != 1 || len(manifest[0].Layers) != 2 || manifest[0].RepoTags[0] != "sfops/jar:"+job {
		t.Fatal(manifest)
	}
	if !bytes.Equal(entries["layer/layer.tar"], tarEntries(t, base)["layer/layer.tar"]) {
		t.Fatal("runtime layer changed")
	}
	var config struct {
		Config struct {
			Entrypoint, Cmd  []string
			User, WorkingDir string
			Labels           map[string]string
		}
	}
	if err := json.Unmarshal(entries[manifest[0].Config], &config); err != nil {
		t.Fatal(err)
	}
	if strings.Join(config.Config.Entrypoint, " ") != "java -jar /app/app.jar" || len(config.Config.Cmd) != 0 || config.Config.User != "10001:10001" || config.Config.WorkingDir != "/app" || config.Config.Labels["com.streamfusion.ops.runtime.digest"] != env.ImageDigest || config.Config.Labels["com.streamfusion.ops.jar.sha256"] != jarHash {
		t.Fatal(config)
	}
	layer := tar.NewReader(bytes.NewReader(entries[manifest[0].Layers[1]]))
	if _, err := layer.Next(); err != nil {
		t.Fatal(err)
	}
	header, err := layer.Next()
	if err != nil || header.Name != "app/app.jar" || header.Uid != 10001 || header.Gid != 10001 || header.Mode != 0444 || header.Size != jarSize {
		t.Fatal(header, err)
	}
	gotJar, err := io.ReadAll(layer)
	wantJar, _ := os.ReadFile(jar)
	if err != nil || !bytes.Equal(gotJar, wantJar) {
		t.Fatal("JAR bytes changed", err)
	}
	if _, _, _, err := PackageJavaJar(base, jar, out, baseImage, jarHash, jarSize, job, env); err == nil {
		t.Fatal("overwrote existing artifact")
	}
	leftovers, _ := filepath.Glob(filepath.Join(filepath.Dir(out), ".jar-layer-*"))
	if len(leftovers) != 0 {
		t.Fatal("temporary layer leaked", leftovers)
	}
}

func TestJavaJarRejectsInvalidInputsAndWrongRuntime(t *testing.T) {
	for _, tc := range []struct {
		manifest string
		version  uint16
		code     string
	}{
		{"Manifest-Version: 1.0\n\n", 65, "JAR_MAIN_CLASS_REQUIRED"},
		{"Manifest-Version: 1.0\nMain-Class: \n\n", 65, "JAR_MAIN_CLASS_REQUIRED"},
		{"Manifest-Version: 1.0\n\nName: file\nMain-Class: demo.Main\n", 65, "JAR_MAIN_CLASS_REQUIRED"},
		{"Main-Class: demo.Main\n", 66, "JAVA21_CLASS_VERSION_REQUIRED"},
	} {
		jar, hash, size := jarFixture(t, tc.manifest, tc.version)
		if err := ValidateJavaJar(jar, hash, size); err == nil || err.Error() != tc.code {
			t.Fatal(tc.code, err)
		}
	}
	jar, hash, size := jarFixture(t, "Main-Class: demo.Main\n", 61)
	if err := ValidateJavaJar(jar, strings.Repeat("0", 64), size); err == nil || err.Error() != "JAR_HASH_MISMATCH" {
		t.Fatal(err)
	}
	if err := ValidateJavaJar(jar, hash, size+1); err == nil {
		t.Fatal("size mismatch accepted")
	}
	base, _, _, image := testArchive(t, "amd64", nil)
	env := RuntimeEnvironment{PluginVersion: "17-jre", ImageID: image}
	output := filepath.Join(t.TempDir(), "bad.tar")
	if _, _, _, err := PackageJavaJar(base, jar, output, image, hash, size, uuid.NewString(), env); err == nil {
		t.Fatal("Java17 accepted")
	}
	if _, err := os.Stat(output); !os.IsNotExist(err) {
		t.Fatal("failed package left artifact", err)
	}
}

func TestJavaJarRejectsAliasedCompressedReadAmplification(t *testing.T) {
	var b bytes.Buffer
	w := zip.NewWriter(&b)
	manifest, err := w.Create("META-INF/MANIFEST.MF")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := manifest.Write([]byte("Main-Class: demo.Main\n")); err != nil {
		t.Fatal(err)
	}
	class := []byte{0xca, 0xfe, 0xba, 0xbe, 0, 0, 0, 65}
	// Non-final empty stored DEFLATE blocks consume compressed input without
	// yielding bytes. Two central records below point to this same data.
	var compressed []byte
	for i := 0; i < 256; i++ {
		compressed = append(compressed, 0, 0, 0, 0xff, 0xff)
	}
	compressed = append(compressed, 1, 8, 0, 0xf7, 0xff)
	compressed = append(compressed, class...)
	entry, err := w.CreateRaw(&zip.FileHeader{Name: "demo/Main.class", Method: zip.Deflate, CRC32: crc32.ChecksumIEEE(class), CompressedSize64: uint64(len(compressed)), UncompressedSize64: uint64(len(class))})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := entry.Write(compressed); err != nil {
		t.Fatal(err)
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	raw := b.Bytes()
	endAt := len(raw) - 22 // The fixture has no ZIP comment.
	directoryAt := int(binary.LittleEndian.Uint32(raw[endAt+16:]))
	var duplicate []byte
	for at := directoryAt; at < endAt; {
		nameLen := int(binary.LittleEndian.Uint16(raw[at+28:]))
		recordLen := 46 + nameLen + int(binary.LittleEndian.Uint16(raw[at+30:])) + int(binary.LittleEndian.Uint16(raw[at+32:]))
		if string(raw[at+46:at+46+nameLen]) == "demo/Main.class" {
			duplicate = append([]byte(nil), raw[at:at+recordLen]...)
			copy(duplicate[46:], "demo/Else.class")
			break
		}
		at += recordLen
	}
	if len(duplicate) == 0 {
		t.Fatal("class directory entry missing")
	}
	aliased := append([]byte(nil), raw[:endAt]...)
	aliased = append(aliased, duplicate...)
	aliased = append(aliased, raw[endAt:]...)
	end := aliased[len(aliased)-22:]
	binary.LittleEndian.PutUint16(end[8:], 3)
	binary.LittleEndian.PutUint16(end[10:], 3)
	binary.LittleEndian.PutUint32(end[12:], uint32(endAt-directoryAt+len(duplicate)))
	archive, err := zip.NewReader(bytes.NewReader(aliased), int64(len(aliased)))
	if err != nil {
		t.Fatal(err)
	}
	classes := 0
	for _, entry := range archive.File {
		if !strings.HasSuffix(entry.Name, ".class") {
			continue
		}
		reader, err := entry.Open()
		if err != nil {
			t.Fatal(err)
		}
		data, err := io.ReadAll(reader)
		reader.Close()
		if err != nil || !bytes.Equal(data, class) {
			t.Fatal("alias fixture cannot reproduce repeated reads", err)
		}
		classes++
	}
	if classes != 2 {
		t.Fatal(classes)
	}
	filename := filepath.Join(t.TempDir(), "aliased.jar")
	if err := os.WriteFile(filename, aliased, 0600); err != nil {
		t.Fatal(err)
	}
	hash := sha256.Sum256(aliased)
	if err := ValidateJavaJar(filename, hex.EncodeToString(hash[:]), int64(len(aliased))); err == nil || err.Error() != "JAR_COMPRESSED_READ_LIMIT" {
		t.Fatal("aliased compressed scans not bounded", err)
	}
}
