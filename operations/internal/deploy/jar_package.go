package deploy

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"
)

const JarLimit int64 = 128 << 20

func ValidateJavaJar(filename, expected string, size int64) error {
	if !Hex.MatchString(expected) || size < 1 || size > JarLimit {
		return errors.New("JAR_SIZE_OR_HASH_INVALID")
	}
	f, err := os.Open(filename)
	if err != nil {
		return err
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil || !info.Mode().IsRegular() || info.Size() != size {
		return errors.New("JAR_SIZE_MISMATCH")
	}
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return err
	}
	if hex.EncodeToString(h.Sum(nil)) != expected {
		return errors.New("JAR_HASH_MISMATCH")
	}
	archive, err := zip.NewReader(f, size)
	if err != nil || len(archive.File) > 50000 {
		return errors.New("JAR_ARCHIVE_INVALID")
	}
	main := false
	var expanded, compressed uint64
	seen := map[string]bool{}
	for _, entry := range archive.File {
		if seen[entry.Name] {
			return errors.New("JAR_DUPLICATE_ENTRY")
		}
		seen[entry.Name] = true
		// ZIP central-directory entries may alias the same compressed range.
		// Bound the total declared compressed reads, not just expanded bytes,
		// before opening each entry so repeated class-header scans stay bounded.
		if entry.CompressedSize64 > uint64(size)-compressed {
			return errors.New("JAR_COMPRESSED_READ_LIMIT")
		}
		compressed += entry.CompressedSize64
		if entry.UncompressedSize64 > 1<<30-expanded {
			return errors.New("JAR_EXPANSION_LIMIT")
		}
		expanded += entry.UncompressedSize64
		if entry.Name == "META-INF/MANIFEST.MF" {
			if entry.UncompressedSize64 > 64<<10 {
				return errors.New("JAR_MANIFEST_INVALID")
			}
			reader, err := entry.Open()
			if err != nil {
				return errors.New("JAR_ARCHIVE_INVALID")
			}
			data, err := io.ReadAll(io.LimitReader(reader, 64<<10+1))
			reader.Close()
			if err != nil || len(data) > 64<<10 {
				return errors.New("JAR_MANIFEST_INVALID")
			}
			// Only main-section attributes apply to java -jar. A named-entry
			// section or an empty Main-Class must not make an ordinary ZIP executable.
			section := strings.SplitN(strings.ReplaceAll(string(data), "\r\n", "\n"), "\n\n", 2)[0]
			for _, line := range strings.Split(section, "\n") {
				if strings.HasPrefix(strings.ToLower(line), "main-class: ") && strings.TrimSpace(line[len("Main-Class: "):]) != "" {
					main = true
				}
			}
		}
		if strings.HasSuffix(entry.Name, ".class") && !strings.HasPrefix(entry.Name, "META-INF/versions/") {
			reader, err := entry.Open()
			if err != nil {
				return errors.New("JAR_ARCHIVE_INVALID")
			}
			var header [8]byte
			_, err = io.ReadFull(reader, header[:])
			reader.Close()
			if err != nil || binary.BigEndian.Uint32(header[:4]) != 0xcafebabe {
				return errors.New("JAR_CLASS_INVALID")
			}
			if binary.BigEndian.Uint16(header[6:]) > 65 {
				return errors.New("JAVA21_CLASS_VERSION_REQUIRED")
			}
		}
	}
	if !main {
		return errors.New("JAR_MAIN_CLASS_REQUIRED")
	}
	return nil
}

// Add a fixed, passive app.jar layer to the verified Java21 runtime image.
// No JAR, Dockerfile, Maven plugin, lifecycle script or downloaded code executes.
func PackageJavaJar(baseFile, jarFile, output, baseImageID, jarSHA string, jarSize int64, jobID string, environment RuntimeEnvironment) (string, string, int64, error) {
	fail := func(err error) (string, string, int64, error) { return "", "", 0, err }
	if !ID.MatchString(jobID) || environment.PluginVersion != "21-jre" || environment.ImageID != baseImageID {
		return fail(errors.New("JAVA21_ENVIRONMENT_REQUIRED"))
	}
	if err := ValidateJavaJar(jarFile, jarSHA, jarSize); err != nil {
		return fail(err)
	}
	base, err := os.Open(baseFile)
	if err != nil {
		return fail(err)
	}
	defer base.Close()
	info, err := base.Stat()
	if err != nil || info.Size() > ArchiveLimit {
		return fail(errors.New("RUNTIME_ARCHIVE_LIMIT"))
	}
	h := sha256.New()
	if _, err := io.Copy(h, base); err != nil {
		return fail(err)
	}
	actual, err := ValidateRuntimeArchive(baseFile, hex.EncodeToString(h.Sum(nil)), info.Size())
	if err != nil || actual != baseImageID {
		return fail(errors.New("RUNTIME_ARCHIVE_IDENTITY_MISMATCH"))
	}
	var manifest []struct {
		Config   string
		RepoTags []string
		Layers   []string
	}
	var oldConfig []byte
	base.Seek(0, 0)
	reader := tar.NewReader(base)
	metadata := map[string][]byte{}
	for {
		header, err := reader.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return fail(err)
		}
		if header.Name == "manifest.json" || strings.HasSuffix(header.Name, ".json") || strings.HasPrefix(header.Name, "blobs/sha256/") && header.Size <= 4<<20 {
			if header.Size <= 4<<20 {
				data, err := io.ReadAll(reader)
				if err != nil {
					return fail(err)
				}
				metadata[header.Name] = data
			}
		}
	}
	if json.Unmarshal(metadata["manifest.json"], &manifest) != nil || len(manifest) != 1 {
		return fail(errors.New("RUNTIME_ARCHIVE_MANIFEST_INVALID"))
	}
	oldConfigName := manifest[0].Config
	oldConfig = metadata[oldConfigName]
	var config map[string]any
	if json.Unmarshal(oldConfig, &config) != nil {
		return fail(errors.New("RUNTIME_CONFIG_INVALID"))
	}
	container, _ := config["config"].(map[string]any)
	if container == nil {
		container = map[string]any{}
	}
	container["Entrypoint"] = []string{"java", "-jar", "/app/app.jar"}
	container["Cmd"] = []string{}
	container["User"] = "10001:10001"
	container["WorkingDir"] = "/app"
	labels, _ := container["Labels"].(map[string]any)
	if labels == nil {
		labels = map[string]any{}
	}
	labels["com.streamfusion.ops.jar.sha256"] = jarSHA
	labels["com.streamfusion.ops.runtime.digest"] = environment.ImageDigest
	labels["com.streamfusion.ops.package.recipe"] = "java-jar-v1"
	container["Labels"] = labels
	config["config"] = container
	// The large JAR layer is streamed into a temporary file, never held in RAM.
	layerFile, err := os.CreateTemp(filepath.Dir(output), ".jar-layer-*.tar")
	if err != nil {
		return fail(err)
	}
	layerName := layerFile.Name()
	defer os.Remove(layerName)
	defer layerFile.Close()
	tw := tar.NewWriter(layerFile)
	if err := tw.WriteHeader(&tar.Header{Name: "app", Typeflag: tar.TypeDir, Mode: 0755}); err != nil {
		return fail(err)
	}
	if err := tw.WriteHeader(&tar.Header{Name: "app/app.jar", Typeflag: tar.TypeReg, Mode: 0444, Size: jarSize, Uid: 10001, Gid: 10001}); err != nil {
		return fail(err)
	}
	jar, err := os.Open(jarFile)
	if err != nil {
		return fail(err)
	}
	_, err = io.Copy(tw, jar)
	jar.Close()
	if err != nil {
		return fail(err)
	}
	if err = tw.Close(); err != nil {
		return fail(err)
	}
	layerSize, err := layerFile.Seek(0, io.SeekEnd)
	if err != nil {
		return fail(err)
	}
	layerFile.Seek(0, 0)
	layerHash := sha256.New()
	if _, err := io.Copy(layerHash, layerFile); err != nil {
		return fail(err)
	}
	layerFile.Seek(0, 0)
	layerDigest := hex.EncodeToString(layerHash.Sum(nil))
	rootfs, ok := config["rootfs"].(map[string]any)
	if !ok {
		return fail(errors.New("RUNTIME_ROOTFS_INVALID"))
	}
	diffs, ok := rootfs["diff_ids"].([]any)
	if !ok {
		return fail(errors.New("RUNTIME_ROOTFS_INVALID"))
	}
	rootfs["diff_ids"] = append(diffs, "sha256:"+layerDigest)
	history, _ := config["history"].([]any)
	config["history"] = append(history, map[string]any{"created_by": "streamfusion-ops java-jar-v1"})
	delete(config, "container_config")
	delete(config, "container")
	config["created"] = time.Unix(0, 0).UTC().Format(time.RFC3339)
	configData, _ := json.Marshal(config)
	configHash := sha256.Sum256(configData)
	configName := hex.EncodeToString(configHash[:]) + ".json"
	manifest[0].Config = configName
	manifest[0].RepoTags = []string{"sfops/jar:" + jobID}
	manifest[0].Layers = append(manifest[0].Layers, layerDigest+"/layer.tar")
	manifestData, _ := json.Marshal(manifest)
	out, err := os.OpenFile(output, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return fail(err)
	}
	okOutput := false
	defer func() {
		out.Close()
		if !okOutput {
			os.Remove(output)
		}
	}()
	archiveWriter := tar.NewWriter(out)
	base.Seek(0, 0)
	reader = tar.NewReader(base)
	for {
		header, err := reader.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return fail(err)
		}
		if header.Name == "manifest.json" || header.Name == "repositories" || header.Name == "index.json" || header.Name == "oci-layout" || header.Name == oldConfigName {
			continue
		}
		if err := archiveWriter.WriteHeader(header); err != nil {
			return fail(err)
		}
		if _, err := io.Copy(archiveWriter, reader); err != nil {
			return fail(err)
		}
	}
	for _, item := range []struct {
		name   string
		size   int64
		reader io.Reader
	}{{configName, int64(len(configData)), bytes.NewReader(configData)}, {"manifest.json", int64(len(manifestData)), bytes.NewReader(manifestData)}, {layerDigest + "/layer.tar", layerSize, layerFile}} {
		if err := archiveWriter.WriteHeader(&tar.Header{Name: item.name, Typeflag: tar.TypeReg, Mode: 0600, Size: item.size}); err != nil {
			return fail(err)
		}
		if _, err := io.Copy(archiveWriter, item.reader); err != nil {
			return fail(err)
		}
	}
	if err := archiveWriter.Close(); err != nil {
		return fail(err)
	}
	if err := out.Sync(); err != nil {
		return fail(err)
	}
	if err := out.Close(); err != nil {
		return fail(err)
	}
	file, err := os.Open(output)
	if err != nil {
		return fail(err)
	}
	finalHash := sha256.New()
	size, err := io.Copy(finalHash, file)
	file.Close()
	if err != nil {
		return fail(err)
	}
	sha := hex.EncodeToString(finalHash.Sum(nil))
	image, err := ValidateArchive(output, sha, size)
	if err != nil {
		return fail(err)
	}
	okOutput = true
	return image, sha, size, nil
}
