package plugin

import (
	"errors"
	"strings"
)

// Definition is immutable material shipped with the Ops binary. The executor
// receives the selected definition but validates it against the same catalog.
type Definition struct {
	Key            string `json:"key"`
	Version        string `json:"version"`
	Kind           string `json:"kind"`
	Platform       string `json:"platform"`
	Image          string `json:"image"`
	Digest         string `json:"digest"`
	RecipeRevision string `json:"recipeRevision"`
	Command        string `json:"command"`
	Argument       string `json:"argument"`
}

var catalog = []Definition{
	{Key: "java-runtime", Version: "21-jre", Kind: "JAVA_RUNTIME", Platform: "linux/amd64", Image: "docker.io/library/eclipse-temurin:21-jre", Digest: "sha256:cff19e6215689161eb6162c11b86b0c60ddf802164f2eaf48d570f8fb79a36c5", RecipeRevision: "java-runtime-v1", Command: "java", Argument: "-version"},
	{Key: "java-runtime", Version: "17-jre", Kind: "JAVA_RUNTIME", Platform: "linux/amd64", Image: "docker.io/library/eclipse-temurin:17-jre", Digest: "sha256:207ecae0b2b104dfc6dfa763d1e9cd2041cb344800a180e24cb1a28ed533ff90", RecipeRevision: "java-runtime-v1", Command: "java", Argument: "-version"},
	{Key: "maven-jdk-build", Version: "3.9.11-jdk-21", Kind: "MAVEN_JDK_BUILD", Platform: "linux/amd64", Image: "docker.io/library/maven:3.9.11-eclipse-temurin-21-noble", Digest: "sha256:6fdc855a6ed81d288ca7ca37ac6ff5e9308b612485c0801d70b25a858c83d237", RecipeRevision: "maven-jdk-build-v1", Command: "mvn", Argument: "-version"},
}

func All() []Definition {
	result := make([]Definition, len(catalog))
	copy(result, catalog)
	return result
}

func Find(key, version string) (Definition, error) {
	for _, item := range catalog {
		if item.Key == key && item.Version == version {
			return item, nil
		}
	}
	return Definition{}, errors.New("PLUGIN_NOT_FOUND")
}

func Validate(item Definition) error {
	if item.Key == "" || item.Version == "" || item.Kind == "" || item.Platform != "linux/amd64" || item.Image == "" || len(item.Digest) != 71 || item.Digest[:7] != "sha256:" || item.RecipeRevision == "" || item.Command == "" || item.Argument == "" {
		return errors.New("PLUGIN_MANIFEST_INVALID")
	}
	expected, err := Find(item.Key, item.Version)
	if err != nil || expected != item {
		return errors.New("PLUGIN_MANIFEST_UNTRUSTED")
	}
	return nil
}

func VersionMatches(d Definition, text string) bool {
	switch d.Version {
	case "21-jre":
		return strings.Contains(text, `openjdk version "21.`)
	case "17-jre":
		return strings.Contains(text, `openjdk version "17.`)
	case "3.9.11-jdk-21":
		return strings.Contains(text, "Apache Maven 3.9.11") && strings.Contains(text, "Java version: 21.")
	}
	return false
}
