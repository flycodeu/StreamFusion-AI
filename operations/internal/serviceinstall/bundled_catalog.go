package serviceinstall

// These distribution-specific recipes are separate from the frozen source and
// Docker recipes. Their package bytes must never replace an older definition.
func bundledDefinitions() []Definition {
	part := func(name, base, digest string, size int64) NativePart {
		return NativePart{Name: name, URL: base + name, SHA256: digest, ByteSize: size, Format: "DEB"}
	}
	const ubuntu = "https://archive.ubuntu.com/ubuntu/pool/universe/"
	const security = "https://security.ubuntu.com/ubuntu/pool/"
	redis := Definition{Key: "redis", Name: "Redis (Ubuntu 24.04)", Kind: "SERVICE", Category: "DATABASES", Version: "7.0.15-1ubuntu0.24.04.5", Revision: "redis-ubuntu2404-deb-v1", Platform: "linux/amd64", OfficialURL: "https://packages.ubuntu.com/noble-updates/redis-server", RuntimeModes: []string{"NATIVE"}, SourceModes: []string{"ARCHIVE", "ONLINE"}, Port: 6379,
		Native: &NativeDefinition{BuildRecipe: "DEB", RequiredTools: []string{"/usr/bin/dpkg-deb", "/usr/bin/systemctl", "/usr/sbin/useradd", "/usr/sbin/runuser"}, Parts: []NativePart{
			part("liblzf1_3.6-4_amd64.deb", ubuntu+"libl/liblzf/", "253182d4718271565d6c24125f27dd93da56fce34b34d8226615cb3553bbe73f", 7624),
			part("libjemalloc2_5.3.0-2build1_amd64.deb", ubuntu+"j/jemalloc/", "0cccf7701765b5a633b964ed551c2bd1c074cad17daea8fa20a4e9cad0adb5a8", 256148),
			part("redis-tools_7.0.15-1ubuntu0.24.04.5_amd64.deb", security+"universe/r/redis/", "6e5c009ce7761d0a956dafcfcf054ddeecc7a51efcc38215f26bb5794a4e93e1", 1166518),
			part("redis-server_7.0.15-1ubuntu0.24.04.5_amd64.deb", security+"universe/r/redis/", "5f41607fb77674078c013b2a6196a748c3a5f6ec4bd3201cd08478dce3e26d05", 51686),
		}}}
	nginx := Definition{Key: "nginx", Name: "Nginx (Ubuntu 24.04)", Kind: "SERVICE", Category: "NETWORK", Version: "1.24.0-2ubuntu7.19", Revision: "nginx-ubuntu2404-deb-v1", Platform: "linux/amd64", OfficialURL: "https://packages.ubuntu.com/noble-updates/nginx", RuntimeModes: []string{"NATIVE"}, SourceModes: []string{"ARCHIVE", "ONLINE"}, Port: 80,
		Native: &NativeDefinition{BuildRecipe: "DEB", RequiredTools: []string{"/usr/bin/dpkg-deb", "/usr/bin/systemctl", "/usr/sbin/useradd", "/usr/sbin/runuser"}, Parts: []NativePart{
			part("nginx-common_1.24.0-2ubuntu7.19_all.deb", security+"main/n/nginx/", "e105e2ea8029bed125c0185e414f5e67e0a4c1e19de48653ac5c9f1cbb32617c", 45340),
			part("nginx_1.24.0-2ubuntu7.19_amd64.deb", security+"main/n/nginx/", "b9c3476a504c963d8c3cab6540127fa4100ccf6ddf8ee978780bb32f128968d5", 524926),
		}}}
	redis.Ports = recipePorts(redis.Key, redis.Port)
	nginx.Ports = recipePorts(nginx.Key, nginx.Port)
	java := Definition{Key: "java", Name: "Java", Kind: "TOOL", Category: "RUNTIMES", Version: "8", Revision: "java-8u504b01-native-v1", Platform: "linux/amd64", OfficialURL: "https://adoptium.net/temurin/releases/?version=8", RuntimeModes: []string{"NATIVE"}, SourceModes: []string{"ARCHIVE", "ONLINE"},
		Native: &NativeDefinition{BuildRecipe: "JDK", Parts: []NativePart{{Name: "OpenJDK8U-jdk_x64_linux_hotspot_8u504b01.tar.gz", URL: "https://github.com/adoptium/temurin8-binaries/releases/download/jdk8u504-b01/OpenJDK8U-jdk_x64_linux_hotspot_8u504b01.tar.gz", SHA256: "9c70e102f527ac674ac2fe9c7d47b9a04e2d19842ba5ab8e9b33f368bbadfaea", ByteSize: 103542511, Format: "TAR_GZIP", StripComponents: 1}}}}
	const dockerBase = "https://download.docker.com/linux/ubuntu/dists/noble/pool/stable/amd64/"
	docker := Definition{Key: "docker", Name: "Docker Engine (Ubuntu 24.04)", Kind: "SERVICE", Category: "RUNTIMES", Version: "29.9.2-ubuntu24.04", Revision: "docker-ce-ubuntu2404-deb-v1", Platform: "linux/amd64", OfficialURL: "https://docs.docker.com/engine/install/ubuntu/", RuntimeModes: []string{"NATIVE"}, SourceModes: []string{"ARCHIVE", "ONLINE"},
		Native: &NativeDefinition{BuildRecipe: "DEB_PACKAGE", RequiredTools: []string{"/usr/bin/dpkg", "/usr/bin/dpkg-query", "/usr/bin/dpkg-deb", "/usr/bin/apt-get", "/usr/bin/systemctl"}, Parts: []NativePart{
			part("containerd.io_2.4.2-3~ubuntu.24.04~noble_amd64.deb", dockerBase, "f811ab57d1f4bb0cce19df7d526da8c711d8ebea0447cc83da1999188e664122", 23271468),
			part("docker-ce-cli_29.9.2-1~ubuntu.24.04~noble_amd64.deb", dockerBase, "74747b5c99000279b9b2c7eb82543b4121d8436a58aa3be7e78ac6681d74794f", 17595236),
			part("docker-ce_29.9.2-1~ubuntu.24.04~noble_amd64.deb", dockerBase, "95ddf7a2d8b8c0eb82d8597d4d604e294dcf741081f2e228524d5ab9bfd7c8ae", 24558208),
			part("docker-buildx-plugin_0.38.1-1~ubuntu.24.04~noble_amd64.deb", dockerBase, "642e9a8a0d115a8a57ad7045a4c7c2561522331197f0c087a762de1b72591672", 17800928),
			part("docker-compose-plugin_5.6.1-1~ubuntu.24.04~noble_amd64.deb", dockerBase, "9725be1df7fd5ccc6b21ac24112885896c8d134345e3f4aa20b3c2064ef4620e", 8083388),
		}}}
	return []Definition{redis, nginx, java, docker}
}
