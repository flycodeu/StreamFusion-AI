package com.streamfusion.platform.media.preview.node.zlm;

import com.streamfusion.platform.media.preview.PreviewFailure;
import com.streamfusion.platform.media.preview.node.PreviewNodeProperties;
import java.io.IOException;
import java.nio.file.Files;
import java.nio.file.LinkOption;
import java.nio.file.Path;
import java.nio.file.attribute.AclEntry;
import java.nio.file.attribute.AclEntryPermission;
import java.nio.file.attribute.AclEntryType;
import java.nio.file.attribute.FileAttribute;
import java.nio.file.attribute.PosixFilePermissions;
import java.util.EnumSet;

/** Keeps direct ZLM secrets in a service-owned directory, independent of node-package tokens. */
final class ZlmDirectCredentials {
    private final PreviewNodeProperties properties;
    private final Path home;

    ZlmDirectCredentials(PreviewNodeProperties properties) {
        this(properties, Path.of(System.getProperty("user.home")));
    }

    ZlmDirectCredentials(PreviewNodeProperties properties, Path home) {
        this.properties = properties;
        this.home = home;
    }

    boolean available() {
        try {
            directory();
            return true;
        } catch (PreviewFailure unavailable) {
            return false;
        }
    }

    Path directory() {
        try {
            String configured = properties.credentialDirectory();
            Path directory =
                    configured.isBlank()
                            ? home.resolve(".streamfusion").resolve("preview-zlm-secrets")
                            : Path.of(configured);
            if (!directory.isAbsolute()) throw new IOException("Credential path must be absolute");
            if (configured.isBlank() && !Files.exists(directory, LinkOption.NOFOLLOW_LINKS)) {
                Files.createDirectories(directory.getParent());
                createPrivateDirectory(directory);
            }
            if (!Files.isDirectory(directory, LinkOption.NOFOLLOW_LINKS)
                    || !Files.isWritable(directory))
                throw new IOException("Credential directory unavailable");
            return directory.toRealPath();
        } catch (IOException | RuntimeException unavailable) {
            throw new PreviewFailure("NODE_CREDENTIAL_DIRECTORY_MISSING");
        }
    }

    private static void createPrivateDirectory(Path directory) throws IOException {
        var store = Files.getFileStore(directory.getParent());
        if (store.supportsFileAttributeView("posix")) {
            Files.createDirectory(
                    directory,
                    PosixFilePermissions.asFileAttribute(
                            PosixFilePermissions.fromString("rwx------")));
        } else if (store.supportsFileAttributeView("acl")) {
            var owner =
                    directory
                            .getFileSystem()
                            .getUserPrincipalLookupService()
                            .lookupPrincipalByName(System.getProperty("user.name"));
            var onlyOwner =
                    AclEntry.newBuilder()
                            .setType(AclEntryType.ALLOW)
                            .setPrincipal(owner)
                            .setPermissions(EnumSet.allOf(AclEntryPermission.class))
                            .build();
            FileAttribute<java.util.List<AclEntry>> acl =
                    new FileAttribute<>() {
                        public String name() {
                            return "acl:acl";
                        }

                        public java.util.List<AclEntry> value() {
                            return java.util.List.of(onlyOwner);
                        }
                    };
            Files.createDirectory(directory, acl);
        } else {
            throw new IOException("No protected directory permissions available");
        }
    }
}
