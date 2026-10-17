package com.streamfusion.platform.media.preview.node;

import static org.assertj.core.api.Assertions.*;

import com.fasterxml.jackson.databind.JsonNode;
import com.fasterxml.jackson.databind.ObjectMapper;
import com.streamfusion.platform.media.preview.PreviewDeadline;
import com.streamfusion.platform.media.preview.PreviewProperties;
import com.streamfusion.platform.media.preview.execution.PreviewExecutor.SourceKey;
import com.streamfusion.platform.support.IdentitySchema;
import com.sun.net.httpserver.HttpServer;
import java.net.InetSocketAddress;
import java.net.URI;
import java.nio.charset.StandardCharsets;
import java.nio.file.Files;
import java.nio.file.Path;
import java.time.Duration;
import java.util.List;
import java.util.UUID;
import java.util.concurrent.CompletableFuture;
import java.util.concurrent.Executors;
import java.util.concurrent.TimeUnit;
import org.h2.jdbcx.JdbcDataSource;
import org.junit.jupiter.api.Test;
import org.junit.jupiter.api.condition.EnabledIfEnvironmentVariable;
import org.springframework.jdbc.core.JdbcTemplate;

/**
 * Explicit opt-in. A real remote node and generated fixture/h264, fixture/h265 streams are
 * required.
 */
@EnabledIfEnvironmentVariable(named = "SF_PREVIEW_REMOTE_BROWSER_TEST", matches = "true")
class RemotePreviewRealMediaTest {
    private final ObjectMapper json = new ObjectMapper().findAndRegisterModules();

    private PreviewDeadline budget() {
        return PreviewDeadline.after(Duration.ofSeconds(18));
    }

    @Test
    void browserReceivesDirectAndTranscodedFramesThenConfirmsRemoteCleanup() throws Exception {
        Path connectionFile = Path.of(System.getenv("SF_PREVIEW_REMOTE_CONNECTION_FILE"));
        Path evidence = Path.of(System.getenv("SF_PREVIEW_REMOTE_EVIDENCE_DIR"));
        Files.createDirectories(evidence);
        var connection = PreviewNodeConnection.read(connectionFile, json);
        var data = new JdbcDataSource();
        data.setURL("jdbc:h2:mem:" + UUID.randomUUID() + ";MODE=MySQL;DB_CLOSE_DELAY=-1");
        IdentitySchema.initialize(data);
        var repository = new PreviewNodeRepository(new JdbcTemplate(data));
        var manager =
                new PreviewNodeManager(
                        repository,
                        new PreviewNodeProperties(connectionFile.toString()),
                        new PreviewProperties(64, 64),
                        json);
        var registered = manager.register(manager.registration());
        manager.changeMode(manager.checkMode("REMOTE", registered.version()));
        var executor = new RemotePreviewExecutor(repository, manager, json);
        var finished = new CompletableFuture<JsonNode>();
        var server = HttpServer.create(new InetSocketAddress("127.0.0.1", 0), 8);
        var workers = Executors.newFixedThreadPool(4);
        server.setExecutor(workers);
        server.createContext(
                "/",
                exchange -> {
                    try {
                        String path = exchange.getRequestURI().getPath();
                        byte[] response;
                        if (path.equals("/") && exchange.getRequestMethod().equals("GET")) {
                            exchange.getResponseHeaders()
                                    .set("Content-Type", "text/html; charset=utf-8");
                            response = page().getBytes(StandardCharsets.UTF_8);
                        } else if (exchange.getRequestMethod().equals("POST")) {
                            String body =
                                    new String(
                                            exchange.getRequestBody().readNBytes(65537),
                                            StandardCharsets.UTF_8);
                            if (body.length() > 65536)
                                throw new IllegalArgumentException("Oversized harness input");
                            Object value;
                            if (path.equals("/start/h264") || path.equals("/start/h265")) {
                                String codec = path.endsWith("h264") ? "h264" : "h265";
                                String id = UUID.randomUUID().toString();
                                executor.prepare(id);
                                executor.attachSource(
                                        id,
                                        new SourceKey(codec.equals("h264") ? 1L : 2L, "fixture-v1"),
                                        () -> URI.create("rtsp://127.0.0.1:8554/fixture/" + codec),
                                        () -> {},
                                        budget());
                                var playback =
                                        executor.start(id, List.of("H264"), () -> {}, budget());
                                executor.confirmReady(id, budget());
                                value =
                                        java.util.Map.of(
                                                "id",
                                                id,
                                                "codec",
                                                playback.codec(),
                                                "transcoded",
                                                playback.transcoded());
                            } else if (path.startsWith("/offer/"))
                                value = executor.answer(path.substring(7), body, budget());
                            else if (path.startsWith("/renew/")) {
                                executor.renew(path.substring(7), budget());
                                value = java.util.Map.of("renewed", true);
                            } else if (path.startsWith("/stop/")) {
                                executor.close(path.substring(6), budget());
                                value = java.util.Map.of("closed", true);
                            } else if (path.equals("/result")) {
                                finished.complete(json.readTree(body));
                                value = java.util.Map.of("recorded", true);
                            } else throw new IllegalArgumentException("Unknown harness route");
                            exchange.getResponseHeaders().set("Content-Type", "application/json");
                            response = json.writeValueAsBytes(value);
                        } else {
                            exchange.sendResponseHeaders(404, -1);
                            return;
                        }
                        exchange.sendResponseHeaders(200, response.length);
                        exchange.getResponseBody().write(response);
                    } catch (Exception failure) {
                        var bytes =
                                json.writeValueAsBytes(
                                        java.util.Map.of(
                                                "error",
                                                failure
                                                                instanceof
                                                                com.streamfusion.platform.media
                                                                                .preview
                                                                                .PreviewFailure
                                                                        known
                                                        ? known.reason()
                                                        : "HARNESS_FAILED"));
                        exchange.sendResponseHeaders(500, bytes.length);
                        exchange.getResponseBody().write(bytes);
                    } finally {
                        exchange.close();
                    }
                });
        try {
            server.start();
            json.writeValue(
                    evidence.resolve("browser-harness.json").toFile(),
                    java.util.Map.of(
                            "url",
                            "http://127.0.0.1:" + server.getAddress().getPort(),
                            "nodeId",
                            connection.nodeId()));
            var result = finished.get(240, TimeUnit.SECONDS);
            json.writeValue(evidence.resolve("browser-result.json").toFile(), result);
            assertThat(result.path("error").isMissingNode()).isTrue();
            assertThat(result.path("cases")).hasSize(2);
            for (var item : result.path("cases")) {
                assertThat(item.path("width").asInt()).isEqualTo(640);
                assertThat(item.path("height").asInt()).isEqualTo(360);
                assertThat(item.path("frames").asInt()).isGreaterThan(10);
                assertThat(item.path("bytesReceived").asLong()).isPositive();
                assertThat(item.path("transcoded").asBoolean())
                        .isEqualTo(item.path("source").asText().equals("h265"));
            }
            assertThat(repository.viewers()).isEmpty();
            var snapshot = manager.inspect(repository.node(), budget());
            assertThat(snapshot.resources()).isEmpty();
            assertThat(snapshot.occupiedTranscodes()).isZero();
            json.writeValue(evidence.resolve("node-after.json").toFile(), snapshot);
        } finally {
            server.stop(0);
            workers.shutdownNow();
            for (var viewer : repository.viewers()) executor.close(viewer.id(), budget());
        }
    }

    private String page() {
        return """
            <!doctype html><html lang="zh-CN"><meta charset="utf-8"><title>S2 远程预览媒体验证</title>
            <style>body{font:16px system-ui;background:#f5f7fa;color:#192c42;max-width:1000px;margin:40px auto;padding:20px}button{padding:12px 24px;border:0;border-radius:6px;background:#146b9b;color:white;cursor:pointer}video{display:block;width:640px;max-width:100%;background:#15212c;margin:20px 0}pre{white-space:pre-wrap;background:white;padding:20px;border-radius:8px}</style>
            <h1>S2 远程预览媒体验证</h1><p>隔离测试：Java 远程执行适配 → Linux 节点 → ZLM / FFmpeg → 浏览器</p>
            <button id="run">验证 H.264 直出与 H.265 转码</button><video id="video" autoplay muted playsinline></video><pre id="result">等待开始</pre>
            <script>
            const video=document.getElementById('video'),out=document.getElementById('result');
            async function call(path,body=''){const response=await fetch(path,{method:'POST',body});const value=await response.json();if(!response.ok)throw Error(value.error);return value;}
            const wait=ms=>new Promise(r=>setTimeout(r,ms));
            document.getElementById('run').onclick=async()=>{
              document.getElementById('run').disabled=true;const cases=[];
              try{for(const source of ['h264','h265']){
                out.textContent='正在验证 '+source;const lease=await call('/start/'+source);let pc=new RTCPeerConnection({iceServers:[]});let frames=0;
                pc.ontrack=e=>{video.srcObject=e.streams[0]||new MediaStream([e.track]);video.play().catch(()=>{});};
                const callback=()=>{frames++;if(pc)video.requestVideoFrameCallback(callback);};video.requestVideoFrameCallback(callback);
                const transceiver=pc.addTransceiver('video',{direction:'recvonly'});
                const codecs=RTCRtpReceiver.getCapabilities('video').codecs.filter(c=>/H264|rtx/i.test(c.mimeType));transceiver.setCodecPreferences(codecs);
                await pc.setLocalDescription(await pc.createOffer());
                for(let i=0;i<50&&pc.iceGatheringState!=='complete';i++)await wait(100);
                const answer=await call('/offer/'+lease.id,pc.localDescription.sdp);await pc.setRemoteDescription(answer);
                for(let i=0;i<150&&frames<30;i++)await wait(100);
                const stats=await pc.getStats();let bytesReceived=0,framesDecoded=0;stats.forEach(s=>{if(s.type==='inbound-rtp'&&s.kind==='video'){bytesReceived+=s.bytesReceived||0;framesDecoded+=s.framesDecoded||0;}});
                await call('/renew/'+lease.id);cases.push({source,transcoded:lease.transcoded,width:video.videoWidth,height:video.videoHeight,frames,framesDecoded,bytesReceived});
                pc.close();pc=null;video.srcObject=null;
                for(let attempt=0;;attempt++){try{await call('/stop/'+lease.id);break;}catch(error){if(error.message!=='CLEANUP_PENDING'||attempt>=5)throw error;await wait(1000);}}
                out.textContent=JSON.stringify({cases},null,2);
              }await call('/result',JSON.stringify({cases}));out.textContent='完成：已接收画面并确认清理。\\n'+JSON.stringify({cases},null,2);}
              catch(error){out.textContent='失败：'+error.message;await call('/result',JSON.stringify({error:error.message,cases}));}
            };
            </script></html>
            """;
    }
}
