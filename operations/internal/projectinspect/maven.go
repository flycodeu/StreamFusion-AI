package projectinspect

import (
	"encoding/xml"
	"os"
	"path"
	"strings"
)

type projectMavenArtifact struct {
	GroupID    string `xml:"groupId"`
	ArtifactID string `xml:"artifactId"`
	Version    string `xml:"version"`
}

func recognizeProjectJava(root *os.Root, directory string) (Module, bool) {
	file := path.Join(directory, "pom.xml")
	var pom struct {
		XMLName      xml.Name               `xml:"project"`
		ArtifactID   string                 `xml:"artifactId"`
		Packaging    string                 `xml:"packaging"`
		Parent       projectMavenArtifact   `xml:"parent"`
		Plugins      []projectMavenArtifact `xml:"build>plugins>plugin"`
		Dependencies []projectMavenArtifact `xml:"dependencies>dependency"`
		Properties   struct {
			Java    string `xml:"java.version"`
			Release string `xml:"maven.compiler.release"`
			Target  string `xml:"maven.compiler.target"`
		} `xml:"properties"`
	}
	if xml.Unmarshal(projectSourceText(root, file), &pom) != nil || strings.TrimSpace(pom.ArtifactID) == "" || pom.Packaging == "pom" {
		return Module{}, false
	}
	module := Module{
		Name:                   "Java 后端",
		Kind:                   "BACKEND",
		Evidence:               []string{file},
		RecommendedRuntimeMode: "NATIVE",
	}
	environment := []string{"Java"}
	deps := []string{}
	javaLabel := "Java"
	for _, version := range []string{pom.Properties.Release, pom.Properties.Java, pom.Properties.Target} {
		if version = strings.TrimSpace(version); projectVersion.MatchString(version) {
			environment[0] = "JDK " + version
			javaLabel = "Java " + version
			break
		}
	}
	deps = append(deps, javaLabel)
	bootParent := pom.Parent.GroupID == "org.springframework.boot" && pom.Parent.ArtifactID == "spring-boot-starter-parent"
	bootPlugin := false
	for _, plugin := range pom.Plugins {
		bootPlugin = bootPlugin || plugin.GroupID == "org.springframework.boot" && plugin.ArtifactID == "spring-boot-maven-plugin"
	}
	if bootParent || bootPlugin {
		boot := "Spring Boot"
		if bootParent && projectVersion.MatchString(pom.Parent.Version) {
			boot += " " + pom.Parent.Version
		}
		environment = append(environment, boot)
	}
	maven, command := "Maven", "mvn"
	if name := path.Join(directory, "mvnw"); projectSourceFile(root, name, false) {
		maven, command = "Maven Wrapper", "sh mvnw"
		module.Evidence = append(module.Evidence, name)
	} else if name := path.Join(directory, "mvnw.cmd"); projectSourceFile(root, name, false) {
		maven, command = "Maven Wrapper", ".\\mvnw.cmd"
		module.Evidence = append(module.Evidence, name)
	}
	environment = append(environment, maven)
	deps = appendUniqueStrings(deps, "Maven")
	module.TestCommand = command + " -B -ntp test"
	module.BuildCommand = command + " -B -ntp -DskipTests package"
	module.ArtifactPath = "target/*.jar"
	if bootPlugin || bootParent {
		module.StartCommand = command + " spring-boot:run"
		if evidence := projectDocumentedCommand(root, directory, module.StartCommand+" -Dspring-boot.run.profiles=local"); evidence != "" {
			module.StartCommand += " -Dspring-boot.run.profiles=local"
			module.Evidence = append(module.Evidence, evidence)
		}
	}
	var services []string
	var extraDeps []string
	hasActuator := false
	for _, dep := range pom.Dependencies {
		coord := dep.GroupID + ":" + dep.ArtifactID
		switch coord {
		case "com.mysql:mysql-connector-j", "mysql:mysql-connector-java":
			services = append(services, "MySQL")
		case "org.springframework.boot:spring-boot-starter-data-redis", "io.lettuce:lettuce-core", "redis.clients:jedis":
			services = append(services, "Redis")
		case "org.postgresql:postgresql":
			extraDeps = append(extraDeps, "PostgreSQL")
		case "software.amazon.awssdk:s3", "io.minio:minio":
			extraDeps = append(extraDeps, "SeaweedFS")
		case "org.springframework.boot:spring-boot-starter-amqp":
			extraDeps = append(extraDeps, "RabbitMQ")
		case "org.springframework.kafka:spring-kafka":
			extraDeps = append(extraDeps, "Kafka")
		case "org.springframework.boot:spring-boot-starter-data-mongodb":
			extraDeps = append(extraDeps, "MongoDB")
		case "org.springframework.boot:spring-boot-starter-actuator":
			hasActuator = true
		}
	}
	if len(services) > 0 {
		environment = append(environment, "运行依赖线索："+strings.Join(services, "、"))
	}
	deps = appendUniqueStrings(deps, append(services, extraDeps...)...)
	module.Dependencies = deps
	module.Environment = strings.Join(environment, "、")
	if bootParent || bootPlugin {
		spring := inspectSpringConfigFiles(root, directory)
		module.Profiles = spring.profiles
		module.DefaultProfile = spring.defaultProfile
		module.DefaultPort = spring.defaultPort
		if module.DefaultPort == 0 {
			module.DefaultPort = 8080
		}
		module.EnvHints = spring.envHints
		module.ConfigTemplates = spring.templates
		if len(spring.evidence) > 0 {
			module.Evidence = append(module.Evidence, spring.evidence...)
		}
		if hasActuator {
			module.HealthPath = "/actuator/health"
		}
	}
	if projectSourceFile(root, path.Join(directory, "Dockerfile"), false) {
		module.SupportsContainer = true
	}
	return module, true
}
