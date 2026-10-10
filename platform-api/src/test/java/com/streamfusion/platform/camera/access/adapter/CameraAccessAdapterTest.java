package com.streamfusion.platform.camera.access.adapter;

import static org.assertj.core.api.Assertions.*;
import static org.mockito.ArgumentMatchers.*;
import static org.mockito.Mockito.*;

import com.fasterxml.jackson.databind.ObjectMapper;
import com.streamfusion.platform.camera.config.CameraProperties;
import com.streamfusion.platform.camera.service.CameraSourceRules;
import com.sun.net.httpserver.HttpExchange;
import com.sun.net.httpserver.HttpServer;
import java.io.IOException;
import java.net.InetSocketAddress;
import java.net.URI;
import java.nio.charset.StandardCharsets;
import java.util.ArrayList;
import java.util.List;
import java.util.Map;
import java.util.concurrent.TimeUnit;
import java.util.concurrent.atomic.AtomicInteger;
import java.util.concurrent.atomic.AtomicReference;
import org.junit.jupiter.api.AfterEach;
import org.junit.jupiter.api.BeforeEach;
import org.junit.jupiter.api.Test;

/** Local protocol fixtures only. No camera, network discovery, or provider account is contacted. */
class CameraAccessAdapterTest {
    private HttpServer server;
    private CameraSourceRules rules;
    private CameraHttpTransport transport;
    private CameraAccessContext context;
    private final ObjectMapper json = new ObjectMapper();

    @BeforeEach
    void start() throws IOException {
        server = HttpServer.create(new InetSocketAddress("127.0.0.1", 0), 0);
        server.start();
        // Loopback is permitted only in these transport fixtures, never in production policies.
        rules = mock(CameraSourceRules.class);
        when(rules.host(anyString(), anyString()))
                .thenAnswer(invocation -> invocation.getArgument(0));
        transport = new CameraHttpTransport(rules);
        context =
                new CameraAccessContext(
                        URI.create("http://127.0.0.1:" + server.getAddress().getPort()),
                        "fixture",
                        "operator",
                        "secret-keep-private",
                        554,
                        () -> {});
    }

    @AfterEach
    void stop() {
        transport.close();
        server.stop(0);
    }

    @Test
    void onvifPreservesOpaqueTokensAndGroupsByActualVideoSourceWithoutGuessingStreamRole() {
        AtomicInteger authorized = new AtomicInteger();
        var request =
                new CameraAccessContext(
                        context.endpoint(),
                        "fixture",
                        context.username(),
                        context.password(),
                        554,
                        authorized::incrementAndGet);
        List<String> bodies = new ArrayList<>();
        server.createContext(
                "/onvif/device_service",
                exchange -> {
                    String body =
                            new String(
                                    exchange.getRequestBody().readAllBytes(),
                                    StandardCharsets.UTF_8);
                    bodies.add(body);
                    if (body.contains("GetDeviceInformation"))
                        reply(
                                exchange,
                                200,
                                "<GetDeviceInformationResponse><Manufacturer>Vendor</Manufacturer><Model>NVR</Model><FirmwareVersion>1.2</FirmwareVersion><SerialNumber>SN01</SerialNumber></GetDeviceInformationResponse>");
                    else
                        reply(
                                exchange,
                                200,
                                "<GetServicesResponse><Service><Namespace>http://www.onvif.org/ver10/media/wsdl</Namespace><XAddr>"
                                        + context.endpoint()
                                        + "/media</XAddr></Service></GetServicesResponse>");
                });
        server.createContext(
                "/media",
                exchange -> {
                    String body =
                            new String(
                                    exchange.getRequestBody().readAllBytes(),
                                    StandardCharsets.UTF_8);
                    bodies.add(body);
                    if (body.contains("GetProfiles"))
                        reply(
                                exchange,
                                200,
                                "<GetProfilesResponse>"
                                        + onvifProfile("profile-alpha", "source-east", "0")
                                        + onvifProfile("profile-beta", "source-east", "0")
                                        + onvifProfile("profile-gamma", "source-west", "0")
                                        + onvifProfile("profile-crop", "source-east", "100")
                                        + "</GetProfilesResponse>");
                    else
                        reply(
                                exchange,
                                200,
                                "<GetStreamUriResponse><MediaUri><Uri>rtsp://127.0.0.1:554/live</Uri></MediaUri></GetStreamUriResponse>");
                });
        var result = new OnvifCameraAccessAdapter(transport).discover(request);
        assertThat(result.adapterType()).isEqualTo("ONVIF");
        assertThat(result.device().serialNumber()).isEqualTo("SN01");
        assertThat(result.channels()).hasSize(3);
        assertThat(result.channels().getFirst().profiles())
                .extracting(CameraAccessCatalog.Profile::externalKey)
                .containsExactly("profile-alpha", "profile-beta");
        assertThat(result.channels().stream().flatMap(channel -> channel.profiles().stream()))
                .allMatch(profile -> profile.usageHint().equals("UNKNOWN"));
        assertThat(authorized.get()).isEqualTo(3);
        assertThat(bodies)
                .allMatch(
                        body ->
                                body.contains("PasswordDigest")
                                        && !body.contains(context.password()));
        assertThat(bodies).noneMatch(body -> body.contains("GetStreamUri"));
        assertThat(result.channels().getFirst().profiles().getFirst().locator().uri()).isNull();
        assertThat(result.complete()).isTrue();
    }

    @Test
    void onvifFallsBackToCapabilitiesWithoutDependingOnStreamUriSupport() {
        server.createContext(
                "/onvif/device_service",
                exchange -> {
                    String body =
                            new String(
                                    exchange.getRequestBody().readAllBytes(),
                                    StandardCharsets.UTF_8);
                    if (body.contains("GetDeviceInformation"))
                        reply(
                                exchange,
                                200,
                                "<GetDeviceInformationResponse><Model>Device</Model></GetDeviceInformationResponse>");
                    else if (body.contains("GetServices"))
                        reply(exchange, 500, soapFault("ActionNotSupported"));
                    else
                        reply(
                                exchange,
                                200,
                                "<GetCapabilitiesResponse><Capabilities><Media><XAddr>"
                                        + context.endpoint()
                                        + "/media</XAddr></Media></Capabilities></GetCapabilitiesResponse>");
                });
        server.createContext(
                "/media",
                exchange -> {
                    String body =
                            new String(
                                    exchange.getRequestBody().readAllBytes(),
                                    StandardCharsets.UTF_8);
                    if (body.contains("GetProfiles"))
                        reply(
                                exchange,
                                200,
                                "<GetProfilesResponse>"
                                        + onvifProfile("first", "source", "0")
                                        + onvifProfile("second", "source", "0")
                                        + "</GetProfilesResponse>");
                    else if (body.contains(">second<")) reply(exchange, 404, "unsupported");
                    else
                        reply(
                                exchange,
                                200,
                                "<GetStreamUriResponse><Uri>rtsp://127.0.0.1/live</Uri></GetStreamUriResponse>");
                });
        var result = new OnvifCameraAccessAdapter(transport).discover(context);
        assertThat(result.complete()).isTrue();
        assertThat(result.warnings())
                .contains("DEVICE_IDENTITY_UNVERIFIED")
                .doesNotContain("PROFILE_URI_UNAVAILABLE");
        assertThat(result.channels().getFirst().profiles()).hasSize(2);
    }

    @Test
    void media2ReadsConfigurationsWithoutRequestingMediaUrisOrDuplicatingMedia1Catalog() {
        AtomicInteger media1Calls = new AtomicInteger();
        AtomicInteger uriCalls = new AtomicInteger();
        server.createContext(
                "/onvif/device_service",
                exchange -> {
                    String body =
                            new String(
                                    exchange.getRequestBody().readAllBytes(),
                                    StandardCharsets.UTF_8);
                    if (body.contains("GetDeviceInformation"))
                        reply(
                                exchange,
                                200,
                                "<GetDeviceInformationResponse><Manufacturer>Example</Manufacturer><Model>Media2 NVR</Model><SerialNumber>real-sn</SerialNumber></GetDeviceInformationResponse>");
                    else
                        reply(
                                exchange,
                                200,
                                "<GetServicesResponse><Service><Namespace>http://www.onvif.org/ver10/media/wsdl</Namespace><XAddr>"
                                        + context.endpoint()
                                        + "/media1</XAddr></Service><Service><Namespace>http://www.onvif.org/ver20/media/wsdl</Namespace><XAddr>"
                                        + context.endpoint()
                                        + "/media2</XAddr></Service></GetServicesResponse>");
                });
        server.createContext(
                "/media1",
                exchange -> {
                    media1Calls.incrementAndGet();
                    reply(exchange, 500, "must not duplicate");
                });
        server.createContext(
                "/media2",
                exchange -> {
                    String body =
                            new String(
                                    exchange.getRequestBody().readAllBytes(),
                                    StandardCharsets.UTF_8);
                    assertThat(exchange.getRequestHeaders().getFirst("Content-Type"))
                            .contains("http://www.onvif.org/ver20/media/wsdl/");
                    if (body.contains("GetProfiles")) {
                        assertThat(body).contains("<tr2:Type>All</tr2:Type>");
                        reply(
                                exchange,
                                200,
                                "<GetProfilesResponse>"
                                        + media2Profile("h265-primary", "sensor-A")
                                        + media2Profile("h264-secondary", "sensor-A")
                                        + media2Profile("other-view", "sensor-B")
                                        + "</GetProfilesResponse>");
                    } else {
                        uriCalls.incrementAndGet();
                        assertThat(body)
                                .contains("<tr2:Protocol>RTSP</tr2:Protocol>", "<tr2:ProfileToken>")
                                .doesNotContain("RtspUnicast");
                        reply(
                                exchange,
                                200,
                                "<GetStreamUriResponse><Uri>rtsp://127.0.0.1:554/device-live</Uri></GetStreamUriResponse>");
                    }
                });
        var catalog = new OnvifCameraAccessAdapter(transport).discover(context);
        assertThat(catalog.channels()).hasSize(2);
        var profiles = catalog.channels().getFirst().profiles();
        assertThat(profiles)
                .extracting(CameraAccessCatalog.Profile::externalKey)
                .containsExactly("h265-primary", "h264-secondary");
        assertThat(profiles.getFirst().codec()).isEqualTo("H265");
        assertThat(profiles.getFirst().frameRate()).isEqualTo(12.5);
        assertThat(profiles.getFirst().locator().serviceNamespace())
                .isEqualTo("http://www.onvif.org/ver20/media/wsdl");
        assertThat(profiles.getFirst().locator().serviceEndpoint().getPath()).isEqualTo("/media2");
        assertThat(media1Calls.get()).isZero();
        assertThat(uriCalls.get()).isZero();
        assertThat(catalog.complete()).isTrue();
    }

    @Test
    void stableDeviceIdentityChangesWithSerialAndMissingIdentityIsExplicit() {
        var warnings = new java.util.LinkedHashSet<String>();
        var first = CameraCatalogSupport.device("A", "Vendor", "NVR", "v1", "serial-1", warnings);
        var renamed = CameraCatalogSupport.device("B", "Vendor", "NVR", "v2", "serial-1", warnings);
        var replacement =
                CameraCatalogSupport.device("A", "Vendor", "NVR", "v1", "serial-2", warnings);
        assertThat(first.externalKey())
                .isEqualTo(renamed.externalKey())
                .isNotEqualTo(replacement.externalKey());
        assertThat(warnings).isEmpty();
        var unknown = CameraCatalogSupport.device("A", "Vendor", "NVR", null, null, warnings);
        assertThat(unknown.externalKey()).isEqualTo("source-local-device:identity-unknown");
        assertThat(warnings).containsExactly("DEVICE_IDENTITY_UNVERIFIED");
    }

    @Test
    void nativeHikvisionUsesActualInputRelationshipsAndReturnedStreamIds() {
        server.createContext(
                "/ISAPI/System/deviceInfo",
                exchange ->
                        reply(
                                exchange,
                                200,
                                "<DeviceInfo><deviceName>NVR</deviceName><model>DS-test</model><serialNumber>sn</serialNumber><firmwareVersion>v1</firmwareVersion></DeviceInfo>"));
        server.createContext(
                "/ISAPI/Streaming/channels",
                exchange ->
                        reply(
                                exchange,
                                200,
                                "<StreamingChannelList>"
                                        + hikProfile("9901", "east", 1920)
                                        + hikProfile("9902", "east", 1920)
                                        + hikProfile("8101", "west", 640)
                                        + "</StreamingChannelList>"));
        var result = new HikvisionCameraAccessAdapter(transport).discover(context);
        assertThat(result.channels())
                .extracting(CameraAccessCatalog.Channel::externalKey)
                .containsExactly("east", "west");
        assertThat(result.channels().getFirst().profiles()).hasSize(2);
        var profile = result.channels().getFirst().profiles().getFirst();
        assertThat(profile.usageHint()).isEqualTo("UNKNOWN");
        assertThat(profile.frameRate()).isEqualTo(25.0);
        assertThat(profile.locator().uri().getPath()).isEqualTo("/Streaming/Channels/9901");
        assertThat(profile.locator().kind()).isEqualTo("HIKVISION");
    }

    @Test
    void nativeDahuaDoesNotTreatRecordingModesAsAdditionalMainStreams() {
        server.createContext(
                "/cgi-bin/magicBox.cgi",
                exchange ->
                        reply(
                                exchange,
                                200,
                                exchange.getRequestURI().getQuery().contains("getSoftwareVersion")
                                        ? "version=V3\r\n"
                                        : "deviceType=NVR-test\r\nserialNumber=SN-DH\r\n"));
        server.createContext(
                "/cgi-bin/configManager.cgi",
                exchange ->
                        reply(
                                exchange,
                                200,
                                dahuaProfile(0, "MainFormat", 0, true)
                                        + dahuaProfile(0, "MainFormat", 1, true)
                                        + dahuaProfile(0, "ExtraFormat", 0, true)
                                        + dahuaProfile(0, "ExtraFormat", 1, false)
                                        + dahuaProfile(1, "MainFormat", 0, true)
                                        + dahuaProfile(1, "ExtraFormat", 0, false)
                                        + "table.Encode[0].ExtraFormat[2].Video.Width=1920\r\n"));
        var result = new DahuaCameraAccessAdapter(transport).discover(context);
        assertThat(result.device().firmware()).isEqualTo("V3");
        assertThat(result.channels()).hasSize(2);
        assertThat(result.channels().getFirst().profiles())
                .extracting(CameraAccessCatalog.Profile::usageHint)
                .containsExactly("MAIN", "SUB", "THIRD");
        assertThat(result.channels().get(1).profiles()).hasSize(2);
        assertThat(result.complete()).isFalse();
        assertThat(result.warnings())
                .containsExactly("PROFILE_ENABLE_UNVERIFIED", "PROFILE_CONFIGURATION_INCOMPLETE");
        assertThat(result.channels().getFirst().profiles().get(2).locator().uri().getQuery())
                .isEqualTo("channel=1&subtype=2");
        assertThat(result.channels().get(1).profiles().getFirst().locator().uri().getQuery())
                .isEqualTo("channel=2&subtype=0");
    }

    @Test
    void platformLoadsStableIdentitiesAndNeverFetchesOrStoresPlaybackUrls() {
        AtomicInteger calls = new AtomicInteger();
        server.createContext(
                "/artemis/api/resource/v2/camera/search",
                exchange -> {
                    calls.incrementAndGet();
                    assertThat(exchange.getRequestHeaders().getFirst("x-ca-signature"))
                            .isNotBlank();
                    assertThat(exchange.getRequestHeaders().getFirst("Authorization")).isNull();
                    var body = json.readTree(exchange.getRequestBody());
                    assertThat(body.path("pageNo").asInt()).isEqualTo(1);
                    reply(
                            exchange,
                            200,
                            "{\"code\":\"0\",\"data\":{\"total\":2,\"list\":[{\"cameraIndexCode\":\"opaque-A\",\"cameraName\":\"A\",\"encodeDevIndexCode\":\"device-A\",\"encodeDevName\":\"Device A\"},{\"indexCode\":\"opaque-B\",\"name\":\"B\"}]}}");
                });
        var result = new HikPlatformCameraAccessAdapter(transport, json).discover(context);
        assertThat(result.device()).isNull();
        assertThat(result.complete()).isTrue();
        assertThat(result.channels())
                .extracting(CameraAccessCatalog.Channel::externalKey)
                .containsExactly("opaque-A", "opaque-B");
        assertThat(result.channels()).allMatch(channel -> channel.profiles().isEmpty());
        assertThat(result.deviceFor(result.channels().getFirst()).externalKey())
                .isEqualTo("device-A");
        assertThat(result.deviceFor(result.channels().getLast())).isNull();
        assertThat(result.warnings()).isEmpty();
        assertThat(result.page().hasMore()).isFalse();
        assertThat(calls.get()).isEqualTo(1);
    }

    @Test
    void platformSignatureMatchesLocalOfficialArtemisSdk1115Vector() {
        // Generated with com.hikvision.ga:artemis-http-client:1.1.15.RELEASE SignUtil.sign.
        var headers =
                HikPlatformCameraAccessAdapter.signedHeaders(
                        "dummy-key",
                        "dummy-secret",
                        "/artemis/api/resource/v2/camera/search",
                        "1700000000000",
                        "fixture-nonce");
        assertThat(headers.get("x-ca-signature"))
                .isEqualTo("sP/ban0w3gG0Ry6K+bs5nezbqAKveSG5PBxPFqkVqZk=");
        assertThat(headers.get("x-ca-signature-headers"))
                .isEqualTo("x-ca-key,x-ca-nonce,x-ca-timestamp");
    }

    @Test
    void digestMatchesRfc2617ExampleAndRejectsUnsupportedQop() {
        String header =
                "Digest realm=\"testrealm@host.com\", qop=\"auth,auth-int\", nonce=\"dcd98b7102dd2f0e8b11d0f600bfb0c093\", opaque=\"5ccc069c403ebaf9f0171e9517f40e41\"";
        String value =
                CameraHttpAuthentication.challenge(
                        header,
                        "GET",
                        URI.create("http://host/dir/index.html"),
                        "Mufasa",
                        "Circle Of Life",
                        "0a4f113b");
        assertThat(value).contains("response=\"6629fae49393a05397450978507c4ef1\"");
        assertThatThrownBy(
                        () ->
                                CameraHttpAuthentication.challenge(
                                        header.replace("auth,auth-int", "auth-int"),
                                        "GET",
                                        URI.create("http://host/dir/index.html"),
                                        "Mufasa",
                                        "Circle Of Life"))
                .hasMessage("UNSUPPORTED_AUTHENTICATION");
    }

    @Test
    void platformReadsOnlyRequestedPageAndReportsFurtherPages() {
        AtomicInteger calls = new AtomicInteger();
        server.createContext(
                "/artemis/api/resource/v2/camera/search",
                exchange -> {
                    calls.incrementAndGet();
                    int page = json.readTree(exchange.getRequestBody()).path("pageNo").asInt();
                    var list = new ArrayList<Map<String, String>>();
                    for (int i = 0; i < 100; i++)
                        list.add(Map.of("cameraIndexCode", page + "-" + i));
                    reply(
                            exchange,
                            200,
                            json.writeValueAsString(
                                    Map.of(
                                            "code",
                                            "0",
                                            "data",
                                            Map.of("total", 500, "list", list))));
                });
        var result = new HikPlatformCameraAccessAdapter(transport, json).discover(context);
        assertThat(result.channels()).hasSize(100);
        assertThat(result.complete()).isTrue();
        assertThat(result.page().hasMore()).isTrue();
        assertThat(result.page().total()).isEqualTo(500);
        var second =
                new CameraAccessContext(
                        context.endpoint(),
                        "fixture",
                        context.username(),
                        context.password(),
                        554,
                        () -> {},
                        context.deadlineNanos(),
                        2,
                        100);
        var next = new HikPlatformCameraAccessAdapter(transport, json).discover(second);
        assertThat(next.channels().getFirst().externalKey()).isEqualTo("2-0");
        assertThat(next.page().pageNumber()).isEqualTo(2);
        assertThat(calls.get()).isEqualTo(2);
    }

    @Test
    void digestChallengeRetriesOnceAndChecksAuthorizationBeforeEachRequest() {
        AtomicInteger calls = new AtomicInteger();
        AtomicInteger authChecks = new AtomicInteger();
        AtomicReference<String> authorization = new AtomicReference<>();
        server.createContext(
                "/digest",
                exchange -> {
                    calls.incrementAndGet();
                    String auth = exchange.getRequestHeaders().getFirst("Authorization");
                    if (auth == null) {
                        exchange.getResponseHeaders()
                                .add(
                                        "WWW-Authenticate",
                                        "Digest realm=\"fixture\", nonce=\"nonce\", qop=\"auth\", algorithm=SHA-256");
                        reply(exchange, 401, "challenge");
                    } else {
                        authorization.set(auth);
                        reply(exchange, 200, "ok");
                    }
                });
        var request =
                new CameraAccessContext(
                        context.endpoint(),
                        "fixture",
                        context.username(),
                        context.password(),
                        554,
                        authChecks::incrementAndGet);
        assertThat(
                        new String(
                                transport.open(request).get(context.endpoint().resolve("/digest")),
                                StandardCharsets.UTF_8))
                .isEqualTo("ok");
        assertThat(calls.get()).isEqualTo(2);
        assertThat(authChecks.get()).isEqualTo(2);
        assertThat(authorization.get())
                .startsWith("Digest ")
                .contains("algorithm=SHA-256", "qop=auth", "uri=\"/digest\"")
                .doesNotContain(context.password());
        verify(rules, atLeast(3)).host("127.0.0.1", "fixture");
    }

    @Test
    void revokedActorCannotRetryAuthenticationOrMakeFurtherRequests() {
        AtomicInteger calls = new AtomicInteger();
        server.createContext(
                "/digest",
                exchange -> {
                    calls.incrementAndGet();
                    exchange.getResponseHeaders()
                            .add("WWW-Authenticate", "Basic realm=\"fixture\"");
                    reply(exchange, 401, "challenge");
                });
        AtomicInteger checks = new AtomicInteger();
        var request =
                new CameraAccessContext(
                        context.endpoint(),
                        "fixture",
                        context.username(),
                        context.password(),
                        554,
                        () -> {
                            if (checks.incrementAndGet() == 2)
                                throw new IllegalStateException("revoked");
                        });
        assertThatThrownBy(() -> transport.open(request).get(context.endpoint().resolve("/digest")))
                .isInstanceOf(IllegalStateException.class)
                .hasMessage("revoked");
        assertThat(calls.get()).isEqualTo(1);
    }

    @Test
    void newRegisteredControlDriverContributesOptionsAndAutoOrderWithoutAnotherTypeRegistry() {
        CameraAccessAdapter custom =
                new CameraAccessAdapter() {
                    public String type() {
                        return "EXAMPLE_DEVICE";
                    }

                    public CameraAdapterDescriptor descriptor() {
                        return new CameraAdapterDescriptor(
                                type(),
                                "测试设备",
                                "DEVICE",
                                "DEVICE_LOGIN",
                                true,
                                1,
                                false,
                                "/control",
                                "DEVICE_HTTP");
                    }

                    public CameraAccessCatalog discover(CameraAccessContext request) {
                        return new CameraAccessCatalog(type(), null, List.of(), true, List.of());
                    }
                };
        var registry = new CameraAccessAdapterRegistry(List.of(custom));
        assertThat(registry.methods()).contains("EXAMPLE_DEVICE", "AUTO", "RTSP");
        assertThat(registry.supportsCategory("EXAMPLE_DEVICE", "DEVICE")).isTrue();
        assertThat(registry.descriptor("EXAMPLE_DEVICE").endpointPath()).isEqualTo("/control");
        assertThat(registry.discover("AUTO", context).adapterType()).isEqualTo("EXAMPLE_DEVICE");
    }

    @Test
    void invalidDriverMetadataFailsAtRegistrationBeforeAnyNetworkCall() {
        var invalid =
                List.of(
                        new CameraAdapterDescriptor(
                                "EXAMPLE", "设备", "DEVICE", "UNKNOWN", false, 1, false),
                        new CameraAdapterDescriptor(
                                "EXAMPLE", "平台", "PLATFORM", "DEVICE_LOGIN", false, 1, true),
                        new CameraAdapterDescriptor(
                                "EXAMPLE", "设备", "DEVICE", "DEVICE_LOGIN", false, 1, true),
                        new CameraAdapterDescriptor(
                                "EXAMPLE", " ", "DEVICE", "DEVICE_LOGIN", false, 1, false),
                        new CameraAdapterDescriptor(
                                null, "设备", "DEVICE", "DEVICE_LOGIN", false, 1, false));
        for (var descriptor : invalid) {
            var driver = mock(CameraAccessAdapter.class);
            when(driver.type()).thenReturn("EXAMPLE");
            when(driver.descriptor()).thenReturn(descriptor);
            assertThatThrownBy(() -> new CameraAccessAdapterRegistry(List.of(driver)))
                    .isInstanceOf(IllegalStateException.class)
                    .hasMessage("Invalid camera adapter descriptor");
            verify(driver, never()).discover(any());
        }
    }

    @Test
    void autoPreservesAuthenticationFailureWhenOnvifIsUnsupported() {
        CameraAccessAdapter hik = mock(CameraAccessAdapter.class);
        CameraAccessAdapter dahua = mock(CameraAccessAdapter.class);
        CameraAccessAdapter onvif = mock(CameraAccessAdapter.class);
        when(hik.type()).thenReturn("HIKVISION");
        when(dahua.type()).thenReturn("DAHUA");
        when(onvif.type()).thenReturn("ONVIF");
        when(hik.descriptor())
                .thenReturn(
                        new CameraAdapterDescriptor(
                                "HIKVISION", "Hik", "DEVICE", "DEVICE_LOGIN", true, 10, false));
        when(dahua.descriptor())
                .thenReturn(
                        new CameraAdapterDescriptor(
                                "DAHUA", "Dahua", "DEVICE", "DEVICE_LOGIN", true, 20, false));
        when(onvif.descriptor())
                .thenReturn(
                        new CameraAdapterDescriptor(
                                "ONVIF", "Onvif", "DEVICE", "DEVICE_LOGIN", true, 30, false));
        when(hik.discover(context)).thenThrow(new CameraAdapterException("PROTOCOL_NOT_SUPPORTED"));
        when(dahua.discover(context))
                .thenThrow(new CameraAdapterException("AUTHENTICATION_FAILED"));
        when(onvif.discover(context))
                .thenThrow(new CameraAdapterException("PROTOCOL_NOT_SUPPORTED"));
        var registry = new CameraAccessAdapterRegistry(List.of(hik, dahua, onvif));
        assertThatThrownBy(() -> registry.discover("AUTO", context))
                .hasMessage("AUTHENTICATION_FAILED");
        verify(onvif).discover(context);
    }

    @Test
    void autoIgnoresGenericHttp200PagesAndFindsTheActualOnvifService() {
        var fakeHik =
                new AtomicReference<>("<!doctype html><html><body>Device homepage</body></html>");
        var fakeDahua = new AtomicReference<>("<html>Device homepage</html>");
        server.createContext(
                "/ISAPI/System/deviceInfo", exchange -> reply(exchange, 200, fakeHik.get()));
        server.createContext(
                "/cgi-bin/magicBox.cgi", exchange -> reply(exchange, 200, fakeDahua.get()));
        server.createContext(
                "/onvif/device_service",
                exchange -> {
                    String body =
                            new String(
                                    exchange.getRequestBody().readAllBytes(),
                                    StandardCharsets.UTF_8);
                    if (body.contains("GetDeviceInformation"))
                        reply(
                                exchange,
                                200,
                                "<GetDeviceInformationResponse><Manufacturer>Actual vendor</Manufacturer><Model>ONVIF device</Model><SerialNumber>abc</SerialNumber></GetDeviceInformationResponse>");
                    else
                        reply(
                                exchange,
                                200,
                                "<GetServicesResponse><Service><Namespace>http://www.onvif.org/ver10/media/wsdl</Namespace><XAddr>"
                                        + context.endpoint()
                                        + "/media</XAddr></Service></GetServicesResponse>");
                });
        server.createContext(
                "/media",
                exchange -> {
                    String body =
                            new String(
                                    exchange.getRequestBody().readAllBytes(),
                                    StandardCharsets.UTF_8);
                    if (body.contains("GetProfiles"))
                        reply(
                                exchange,
                                200,
                                "<GetProfilesResponse>"
                                        + onvifProfile("opaque", "sensor", "0")
                                        + "</GetProfilesResponse>");
                    else
                        reply(
                                exchange,
                                200,
                                "<GetStreamUriResponse><Uri>rtsp://127.0.0.1/live</Uri></GetStreamUriResponse>");
                });
        var registry =
                new CameraAccessAdapterRegistry(
                        List.of(
                                new HikvisionCameraAccessAdapter(transport),
                                new DahuaCameraAccessAdapter(transport),
                                new OnvifCameraAccessAdapter(transport)));
        var first = registry.discover("AUTO", context);
        assertThat(first.adapterType()).isEqualTo("ONVIF");
        assertThat(first.device().manufacturer()).isEqualTo("Actual vendor");
        fakeHik.set("<Response><status>ok</status></Response>");
        fakeDahua.set("status=ok\r\nversion=web-ui\r\n");
        assertThat(registry.discover("AUTO", context).adapterType()).isEqualTo("ONVIF");
        fakeHik.set("<DeviceInfo><deviceName>generic placeholder</deviceName></DeviceInfo>");
        fakeDahua.set("deviceType=placeholder\r\n");
        assertThat(registry.discover("AUTO", context).adapterType()).isEqualTo("ONVIF");
    }

    @Test
    void autoDoesNotTryOtherVendorsAfterActualAuthenticationRejection() {
        AtomicInteger hikCalls = new AtomicInteger();
        AtomicInteger dahuaCalls = new AtomicInteger();
        AtomicInteger onvifCalls = new AtomicInteger();
        server.createContext(
                "/ISAPI/System/deviceInfo",
                exchange -> {
                    hikCalls.incrementAndGet();
                    exchange.getResponseHeaders().add("WWW-Authenticate", "Basic realm=\"camera\"");
                    reply(exchange, 401, "Unauthorized");
                });
        server.createContext(
                "/cgi-bin/magicBox.cgi",
                exchange -> {
                    dahuaCalls.incrementAndGet();
                    reply(exchange, 200, "must not call");
                });
        server.createContext(
                "/onvif/device_service",
                exchange -> {
                    onvifCalls.incrementAndGet();
                    // Drain the POST body before closing so the fixture reliably sends its 404.
                    exchange.getRequestBody().readAllBytes();
                    reply(exchange, 404, "ONVIF unavailable");
                });
        var registry =
                new CameraAccessAdapterRegistry(
                        List.of(
                                new HikvisionCameraAccessAdapter(transport),
                                new DahuaCameraAccessAdapter(transport),
                                new OnvifCameraAccessAdapter(transport)));
        assertThatThrownBy(() -> registry.discover("AUTO", context))
                .hasMessage("AUTHENTICATION_FAILED");
        assertThat(hikCalls.get()).isEqualTo(2);
        assertThat(dahuaCalls.get()).isZero();
        assertThat(onvifCalls.get()).isEqualTo(1);
    }

    @Test
    void onvifHttp500NestedAuthenticationFaultStopsBeforeDirectoryRequests() {
        AtomicInteger calls = new AtomicInteger();
        server.createContext(
                "/onvif/device_service",
                exchange -> {
                    calls.incrementAndGet();
                    reply(exchange, 500, soapFault("NotAuthorized"));
                });
        assertThatThrownBy(() -> new OnvifCameraAccessAdapter(transport).discover(context))
                .hasMessage("AUTHENTICATION_FAILED");
        assertThat(calls.get()).isEqualTo(1);
    }

    @Test
    void rejectsRedirectsCrossOriginXmlEntitiesAndOversizedResponses() {
        server.createContext(
                "/redirect",
                exchange -> {
                    exchange.getResponseHeaders()
                            .add("Location", "http://169.254.169.254/metadata");
                    reply(exchange, 302, "");
                });
        server.createContext(
                "/large",
                exchange -> reply(exchange, 200, "x".repeat(CameraHttpTransport.MAX_BODY + 1)));
        assertThatThrownBy(
                        () -> transport.open(context).get(context.endpoint().resolve("/redirect")))
                .hasMessage("REDIRECT_REJECTED");
        assertThatThrownBy(() -> transport.open(context).get(URI.create("http://localhost/x")))
                .hasMessage("CREDENTIAL_ORIGIN_REJECTED");
        assertThatThrownBy(() -> transport.open(context).get(context.endpoint().resolve("/large")))
                .hasMessage("RESPONSE_TOO_LARGE");
        assertThatThrownBy(
                        () ->
                                CameraXml.parse(
                                        "<!DOCTYPE x [<!ENTITY a SYSTEM 'file:///C:/secret'>]><x>&a;</x>"
                                                .getBytes(StandardCharsets.UTF_8)))
                .hasMessage("INVALID_XML_RESPONSE");
    }

    @Test
    void productionPolicyRejectsLocalAddressesAndRequiresApprovedHostsAndCidrs() {
        var properties = new CameraProperties();
        var policy = new CameraProperties.NetworkPolicy();
        policy.setCidrs(List.of("10.0.0.0/8", "fd12:3456::/32"));
        policy.setHosts(List.of("camera.example"));
        properties.setNetworkPolicies(Map.of("plant", policy));
        try (var real = new CloseableTransport(new CameraSourceRules(properties))) {
            real.transport.validate(
                    URI.create("https://camera.example/onvif/device_service"), "plant", false);
            real.transport.validate(URI.create("http://[fd12:3456::2]/"), "plant", false);
            for (String host :
                    List.of(
                            "127.0.0.1",
                            "169.254.169.254",
                            "192.168.1.1",
                            "localhost",
                            "evil.example"))
                assertThatThrownBy(
                                () ->
                                        real.transport.validate(
                                                URI.create("http://" + host), "plant", false))
                        .hasMessage("DESTINATION_REJECTED");
            assertThatThrownBy(
                            () ->
                                    real.transport.validate(
                                            URI.create("http://u:p@10.0.0.1"), "plant", false))
                    .hasMessage("DESTINATION_REJECTED");
        }
    }

    @Test
    void approvedDnsNameCannotBypassResolvedAddressPolicy() {
        AtomicInteger calls = new AtomicInteger();
        server.createContext(
                "/",
                exchange -> {
                    calls.incrementAndGet();
                    reply(exchange, 200, "unsafe");
                });
        when(rules.host(eq("localhost"), eq("fixture"))).thenReturn("localhost");
        when(rules.host(
                        argThat(host -> host.contains(":") || host.equals("127.0.0.1")),
                        eq("fixture")))
                .thenThrow(new IllegalArgumentException("address not in approved CIDRs"));
        var domain =
                new CameraAccessContext(
                        URI.create("http://localhost:" + server.getAddress().getPort()),
                        "fixture",
                        "u",
                        "p",
                        554,
                        () -> {});
        assertThatThrownBy(() -> transport.open(domain).get(domain.endpoint()))
                .hasMessage("DNS_REJECTED");
        assertThat(calls.get()).isZero();
    }

    @Test
    void totalDeadlineIsSharedByAdapterAttempts() {
        var expired =
                new CameraAccessContext(
                        context.endpoint(),
                        "fixture",
                        "u",
                        "p",
                        554,
                        () -> {},
                        System.nanoTime() - TimeUnit.SECONDS.toNanos(1));
        assertThatThrownBy(() -> transport.open(expired).get(context.endpoint()))
                .hasMessage("DISCOVERY_TIMEOUT");
        assertThat(context.toString()).doesNotContain(context.password(), context.username());
        assertThat(
                        new CameraAccessCatalog.Locator(
                                        "RTSP",
                                        URI.create("rtsp://u:p@host/path?secret=1"),
                                        "token",
                                        null)
                                .toString())
                .doesNotContain("secret", "token", "host", "u:p");
    }

    private static String onvifProfile(String token, String source, String x) {
        return "<Profiles token=\""
                + token
                + "\"><Name>"
                + token
                + "</Name><VideoSourceConfiguration><SourceToken>"
                + source
                + "</SourceToken><Bounds x=\""
                + x
                + "\" y=\"0\" width=\"1920\" height=\"1080\"/></VideoSourceConfiguration>"
                + "<VideoEncoderConfiguration><Encoding>H264</Encoding><Resolution><Width>1920</Width><Height>1080</Height></Resolution><RateControl><FrameRateLimit>25</FrameRateLimit><BitrateLimit>2048</BitrateLimit></RateControl></VideoEncoderConfiguration></Profiles>";
    }

    private static String hikProfile(String id, String channel, int width) {
        return "<StreamingChannel><id>"
                + id
                + "</id><channelName>"
                + id
                + "</channelName><enabled>true</enabled><Video><videoInputChannelID>"
                + channel
                + "</videoInputChannelID><videoCodecType>H.264</videoCodecType><videoResolutionWidth>"
                + width
                + "</videoResolutionWidth><maxFrameRate>2500</maxFrameRate></Video></StreamingChannel>";
    }

    private static String media2Profile(String token, String source) {
        return "<Profiles token=\""
                + token
                + "\"><Name>"
                + token
                + "</Name><Configurations><VideoSource token=\"cfg-source\"><SourceToken>"
                + source
                + "</SourceToken><Bounds x=\"0\" y=\"0\" width=\"1920\" height=\"1080\"/></VideoSource>"
                + "<VideoEncoder token=\"cfg-encoder\"><Encoding>H265</Encoding><Resolution><Width>1920</Width><Height>1080</Height></Resolution>"
                + "<RateControl><FrameRateLimit>12.5</FrameRateLimit><BitrateLimit>1024</BitrateLimit></RateControl></VideoEncoder></Configurations></Profiles>";
    }

    private static String soapFault(String reason) {
        return "<s:Envelope xmlns:s=\"http://www.w3.org/2003/05/soap-envelope\" xmlns:ter=\"http://www.onvif.org/ver10/error\">"
                + "<s:Body><s:Fault><s:Code><s:Value>s:Sender</s:Value><s:Subcode><s:Value>ter:"
                + reason
                + "</s:Value></s:Subcode></s:Code><s:Reason><s:Text>Fixture</s:Text></s:Reason></s:Fault></s:Body></s:Envelope>";
    }

    private static String dahuaProfile(int channel, String kind, int index, boolean enabled) {
        String prefix = "table.Encode[" + channel + "]." + kind + "[" + index + "].";
        return prefix
                + "VideoEnable="
                + enabled
                + "\r\n"
                + prefix
                + "Video.Compression=H.264\r\n"
                + prefix
                + "Video.FPS=25\r\n"
                + prefix
                + "Video.Width=1920\r\n"
                + prefix
                + "Video.Height=1080\r\n";
    }

    private static void reply(HttpExchange exchange, int status, String body) throws IOException {
        byte[] bytes = body.getBytes(StandardCharsets.UTF_8);
        exchange.sendResponseHeaders(status, bytes.length);
        try (var output = exchange.getResponseBody()) {
            output.write(bytes);
        }
    }

    private static final class CloseableTransport implements AutoCloseable {
        private final CameraHttpTransport transport;

        private CloseableTransport(CameraSourceRules rules) {
            transport = new CameraHttpTransport(rules);
        }

        @Override
        public void close() {
            transport.close();
        }
    }
}
