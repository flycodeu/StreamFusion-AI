package serviceinstall

// Native recipes contain only immutable vendor sources. SOURCE_BUNDLE contains
// the original bytes of each named part, never an executable install script.
func nativeDefinition(key, version string) *NativeDefinition {
	part := func(name, url, sha string, size int64, format string, strip int) NativePart {
		return NativePart{Name: name, URL: url, SHA256: sha, ByteSize: size, Format: format, StripComponents: strip}
	}
	jdk21 := part("jdk.tar.gz", "https://github.com/adoptium/temurin21-binaries/releases/download/jdk-21.0.12.1%2B1/OpenJDK21U-jdk_x64_linux_hotspot_21.0.12.1_1.tar.gz", "ce79869e1307ed8ee1e2baa86a412b1eb5b75d10a01006d788a6f968bcfaee94", 207473347, "TAR_GZIP", 1)
	result := &NativeDefinition{RequiredTools: []string{"/usr/bin/systemctl", "/usr/sbin/useradd", "/usr/sbin/runuser"}}
	switch key {
	case "seaweedfs":
		result.BuildRecipe = "BINARY"
		result.Parts = []NativePart{part("weed.tar.gz", "https://github.com/seaweedfs/seaweedfs/releases/download/4.48/linux_amd64.tar.gz", "4a7d108384d044d95212d1342cdda9533fa55842c1c9b41f606ca3c8a9561124", 46067365, "TAR_GZIP", 0)}
	case "mediamtx":
		result.BuildRecipe = "BINARY"
		result.Parts = []NativePart{part("mediamtx.tar.gz", "https://github.com/bluenviron/mediamtx/releases/download/v1.21.2/mediamtx_v1.21.2_linux_amd64.tar.gz", "121be6e00397e473a95d83c75f4fcd3819a6af3b0e1a50889cba43ab4fab6a27", 28345997, "TAR_GZIP", 0)}
	case "java":
		result.BuildRecipe = "JDK"
		result.RequiredTools = nil
		result.Parts = []NativePart{jdk21}
		if version == "17" {
			result.Parts = []NativePart{part("jdk.tar.gz", "https://github.com/adoptium/temurin17-binaries/releases/download/jdk-17.0.20.1%2B1/OpenJDK17U-jdk_x64_linux_hotspot_17.0.20.1_1.tar.gz", "3808d1d15e3ec6bd5b84057fb5d84c33d8a1536a258146bcea2e603fc726e08e", 193252603, "TAR_GZIP", 1)}
		}
	case "maven":
		result.BuildRecipe = "MAVEN"
		result.RequiredTools = nil
		result.Parts = []NativePart{part("maven.tar.gz", "https://archive.apache.org/dist/maven/maven-3/3.9.11/binaries/apache-maven-3.9.11-bin.tar.gz", "4b7195b6a4f5c81af4c0212677a32ee8143643401bc6e1e8412e6b06ea82beac", 9160848, "TAR_GZIP", 1), jdk21}
	case "mysql":
		result.BuildRecipe = "MYSQL_BINARY"
		result.RequiredTools = append(result.RequiredTools, "/usr/bin/xz")
		result.Parts = []NativePart{part("mysql.tar.xz", "https://cdn.mysql.com/archives/mysql-8.4/mysql-8.4.11-linux-glibc2.28-x86_64-minimal.tar.xz", "383f54e124d5f325d67f0c6912a8f96814eedc761a17ea30112e52fa4cc6b143", 79777440, "TAR_XZ", 1)}
	case "redis":
		result.BuildRecipe = "REDIS_SOURCE"
		result.RequiredTools = append(result.RequiredTools, "/usr/bin/make", "/usr/bin/cc", "/usr/bin/pkg-config")
		result.Parts = []NativePart{part("redis.tar.gz", "https://codeload.github.com/redis/redis/tar.gz/aaf0ce63b3239f4b51f86ca1da8711b055721993", "dcaac6de6d364c1bebc60eba4165d19b5ea5a55f28bd4eb981e09710a2a7e1ae", 3591483, "TAR_GZIP", 1)}
	case "nginx":
		result.BuildRecipe = "NGINX_SOURCE"
		result.RequiredTools = append(result.RequiredTools, "/usr/bin/make", "/usr/bin/cc", "/usr/bin/pkg-config")
		result.Parts = []NativePart{part("nginx.tar.gz", "https://nginx.org/download/nginx-1.30.6.tar.gz", "3cf46f5ed7504d460125866b7bea99fb357f31d887e2e63b35503310e9b8b6c8", 1331698, "TAR_GZIP", 1)}
	case "zlm":
		result.BuildRecipe = "ZLM_SOURCE"
		result.RequiredTools = append(result.RequiredTools, "/usr/bin/cmake", "/usr/bin/make", "/usr/bin/c++", "/usr/bin/pkg-config")
		result.Parts = []NativePart{
			part("zlm.tar.gz", "https://codeload.github.com/ZLMediaKit/ZLMediaKit/tar.gz/08329b39bf36c141b0a12c2bcceb819456632d9b", "ddcb127280a4cc90d4dba65c18823093572510d4387f5aaa1c3617cc2dbbcd38", 5121399, "TAR_GZIP", 1),
			part("toolkit.tar.gz", "https://codeload.github.com/ZLMediaKit/ZLToolKit/tar.gz/d7d601200f65f3111dbf9d20ed7f14d2654bbd6e", "0650a9af5a8ad71cdf6fbc1383cb5335eaaa1e8aa5b6b66dd37a2f82f20d148d", 236324, "TAR_GZIP", 1),
			part("jsoncpp.tar.gz", "https://codeload.github.com/open-source-parsers/jsoncpp/tar.gz/ca98c98457b1163cca1f7d8db62827c115fec6d1", "ed9259c30e450a1e86087f351eb84d85b5393e21cd5bfa9313b1dceed71ec7c8", 214611, "TAR_GZIP", 1),
			part("media-server.tar.gz", "https://codeload.github.com/ireader/media-server/tar.gz/21c4451ff2e4c4bb1c817e606c8b4e5deac1e719", "ac91fbd9f815e8869a889f809ff4a68a21fe1c5251b1bb100ffac6d793d26151", 821755, "TAR_GZIP", 1),
			part("pybind11.tar.gz", "https://codeload.github.com/pybind/pybind11/tar.gz/ed5057ded698e305210269dafa57574ecf964483", "74369c05d78432723773579628d51299c5553e2039571f785bec74cc7afe1ca2", 915677, "TAR_GZIP", 1),
			part("webassist.tar.gz", "https://codeload.github.com/1002victor/zlm_webassist/tar.gz/6689195ac89462d40accd88f13dfde58902da57b", "6cd8ac1bf8615d69b463fba5300c278153906ac05d26f76356fa3473348bfedd", 917496, "TAR_GZIP", 1),
		}
	default:
		return nil
	}
	return result
}
