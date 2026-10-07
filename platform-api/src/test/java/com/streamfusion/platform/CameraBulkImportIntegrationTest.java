package com.streamfusion.platform;

import static org.assertj.core.api.Assertions.*;
import static org.mockito.Mockito.*;
import static org.springframework.test.web.servlet.request.MockMvcRequestBuilders.*;

import com.fasterxml.jackson.databind.JsonNode;
import com.streamfusion.platform.camera.access.adapter.*;
import com.streamfusion.platform.camera.access.service.*;
import com.streamfusion.platform.common.exception.BusinessException;
import com.streamfusion.platform.support.CameraTestSupport;
import java.util.*;
import java.util.stream.IntStream;
import org.junit.jupiter.api.Test;
import org.springframework.beans.factory.annotation.Autowired;
import org.springframework.test.context.bean.override.mockito.MockitoBean;

/**
 * Actual HTTP + SQL transactions, using deterministic catalogues instead of a physical platform.
 */
class CameraBulkImportIntegrationTest extends CameraTestSupport {
    @MockitoBean CameraAccessWorker worker;
    @MockitoBean CameraJobActor actors;
    @Autowired CameraAccessJobsService jobs;
    @Autowired CameraBulkImportService bulk;

    @Test
    void imports101AcrossPagesAndReplaysStartWithoutNewSourceOrHugeResult() throws Exception {
        var auth = admin();
        var first = page(1, 0, 100, 101, true);
        var discovered = discover(auth, first, null, null);
        var command = command(auth, discovered, null);
        var started = write(post(url(discovered) + "/bulk-import"), auth, command, 202);
        var work = jobs.claim(started.path("jobId").asLong());
        work = process(work, first);
        assertThat(work).isNotNull();
        assertThat(process(work, page(2, 100, 1, 101, false))).isNull();
        var result = read(url(started), auth);
        assertThat(result.path("status").asText()).isEqualTo("SUCCEEDED");
        assertThat(result.path("bulk").path("createdCount").asInt()).isEqualTo(101);
        assertThat(result.path("bulk").path("processedCount").asInt()).isEqualTo(101);
        assertThat(result.path("sourceVersion").asText()).isEqualTo("0");
        assertThat(result.path("candidates")).isEmpty();
        assertThat(result.toString()).doesNotContain("app-secret", "app-key", "camera-100");
        assertThat(write(post(url(started) + "/bulk-import"), auth, command, 202))
                .isEqualTo(result);
        assertThat(jdbc.queryForObject("SELECT COUNT(*) FROM camera_source", Integer.class))
                .isOne();
        assertThat(jdbc.queryForObject("SELECT COUNT(*) FROM camera_channel", Integer.class))
                .isEqualTo(101);
        assertThat(
                        jdbc.queryForObject(
                                "SELECT secret_ciphertext FROM camera_access_job WHERE id=?",
                                byte[].class,
                                started.path("jobId").asLong()))
                .isNull();
        assertThat(read("/camera-access/jobs", auth).path("items")).hasSize(1);
        assertThat(read(url(started) + "/import-items?page=2&size=100", auth).path("items"))
                .hasSize(1);
        var retry =
                discover(
                        auth,
                        page(1, 0, 2, 2, false),
                        result.path("sourceId").asText(),
                        result.path("sourceVersion").asText());
        var retryStarted =
                write(post(url(retry) + "/bulk-import"), auth, command(auth, retry, null), 202);
        process(jobs.claim(retryStarted.path("jobId").asLong()), page(1, 0, 2, 2, false));
        assertThat(read(url(retry), auth).path("bulk").path("existingCount").asInt()).isEqualTo(2);
        assertThat(jdbc.queryForObject("SELECT COUNT(*) FROM camera_channel", Integer.class))
                .isEqualTo(101);
    }

    @Test
    void itemFailureRollsBackOnlyThatItemAndDuplicatePageIdentityIsCountedOnce() throws Exception {
        var auth = admin();
        var malformed = new CameraAccessCatalog.Channel("bad", "不能自动映射", List.of(), true);
        var first =
                new CameraAccessCatalog(
                        "HIK_PLATFORM",
                        null,
                        List.of(channel(0), malformed),
                        true,
                        List.of(),
                        new CameraAccessCatalog.Page(1, 100, 101L, true));
        var started = start(auth, first, null);
        var work = bulk.beginPage(jobs.claim(started.path("jobId").asLong()), first);
        work = bulk.importItem(work, first, 0);
        var failedWork = work;
        assertThatThrownBy(() -> bulk.importItem(failedWork, first, 1))
                .isInstanceOf(BusinessException.class);
        work = bulk.failedItem(work, first, 1, "IMPORT_ITEM_INVALID");
        work = bulk.finishPage(work, first);
        var last =
                new CameraAccessCatalog(
                        "HIK_PLATFORM",
                        null,
                        List.of(channel(0), channel(1)),
                        true,
                        List.of(),
                        new CameraAccessCatalog.Page(2, 100, 101L, false));
        process(work, last);
        var result = read(url(started), auth);
        assertThat(result.path("status").asText()).isEqualTo("PARTIAL");
        assertThat(result.path("bulk").path("createdCount").asInt()).isEqualTo(2);
        assertThat(result.path("bulk").path("failedCount").asInt()).isOne();
        assertThat(result.path("bulk").path("processedCount").asInt()).isEqualTo(3);
        assertThat(result.path("bulk").path("duplicateCount").asInt()).isOne();
        assertThat(read(url(started) + "/import-items?status=FAILED", auth).path("items"))
                .hasSize(1);
        assertThat(jdbc.queryForObject("SELECT COUNT(*) FROM camera_channel", Integer.class))
                .isEqualTo(2);
    }

    @Test
    void cancellationFencesWorkerAndKeepsCommittedRowsAndReceipts() throws Exception {
        var auth = admin();
        var catalog = page(1, 0, 2, 2, false);
        var started = start(auth, catalog, null);
        var work = bulk.beginPage(jobs.claim(started.path("jobId").asLong()), catalog);
        work = bulk.importItem(work, catalog, 0);
        write(post(url(started) + "/cancel"), auth, Map.of(), 200);
        var fenced = work;
        assertThatThrownBy(() -> bulk.importItem(fenced, catalog, 1))
                .isInstanceOf(CameraAdapterException.class);
        var result = read(url(started), auth);
        assertThat(result.path("status").asText()).isEqualTo("CANCELLED");
        assertThat(result.path("bulk").path("createdCount").asInt()).isOne();
        assertThat(jdbc.queryForObject("SELECT COUNT(*) FROM camera_channel", Integer.class))
                .isOne();
        assertThat(read(url(started) + "/import-items", auth).path("total").asInt()).isOne();
    }

    @Test
    void scopeSourceAndActorChangesFenceBackgroundImport() throws Exception {
        var auth = admin();
        var catalog = page(1, 0, 2, 2, false);
        var group =
                write(post("/camera-groups"), auth, Map.of("name", "整批目标", "sortOrder", 0), 201);
        var started = start(auth, catalog, group.path("groupId").asText());
        var work = bulk.beginPage(jobs.claim(started.path("jobId").asLong()), catalog);
        work = bulk.importItem(work, catalog, 0);
        jdbc.update(
                "UPDATE camera_group SET version=version+1 WHERE id=?",
                group.path("groupId").asLong());
        var fenced = work;
        assertThatThrownBy(() -> bulk.importItem(fenced, catalog, 1))
                .isInstanceOf(CameraAdapterException.class)
                .extracting(e -> ((CameraAdapterException) e).reasonCode())
                .isEqualTo("IMPORT_SCOPE_CHANGED");
        jdbc.update(
                "UPDATE camera_group SET version=version-1 WHERE id=?",
                group.path("groupId").asLong());
        jdbc.update(
                "UPDATE camera_source SET version=version+1 WHERE id=?",
                started.path("sourceId").asLong());
        assertThatThrownBy(() -> bulk.checkWork(fenced))
                .isInstanceOf(CameraAdapterException.class)
                .extracting(e -> ((CameraAdapterException) e).reasonCode())
                .isEqualTo("SOURCE_CHANGED");
        jdbc.update(
                "UPDATE camera_source SET version=version-1 WHERE id=?",
                started.path("sourceId").asLong());
        doThrow(new CameraAdapterException("ACTOR_REVOKED"))
                .when(actors)
                .check(anyLong(), anyString());
        assertThatThrownBy(() -> bulk.checkWork(fenced))
                .isInstanceOf(CameraAdapterException.class)
                .extracting(e -> ((CameraAdapterException) e).reasonCode())
                .isEqualTo("ACTOR_REVOKED");
        assertThat(jdbc.queryForObject("SELECT COUNT(*) FROM camera_channel", Integer.class))
                .isOne();
    }

    @Test
    void terminalOutcomeSurvivesSecretDeadlineAndCleanupCascadesItems() throws Exception {
        var auth = admin();
        var catalog = page(1, 0, 1, 1, false);
        var started = start(auth, catalog, null);
        process(jobs.claim(started.path("jobId").asLong()), catalog);
        jdbc.update(
                "UPDATE camera_access_job SET expires_at=DATEADD('MINUTE',-1,CURRENT_TIMESTAMP) WHERE id=?",
                started.path("jobId").asLong());
        assertThat(read(url(started), auth).path("status").asText()).isEqualTo("SUCCEEDED");
        assertThat(read(url(started) + "/import-items", auth).path("total").asInt()).isOne();
        var otherSession = login("CameraAdmin", "AdminPass1!");
        assertThat(read("/camera-access/jobs", otherSession).path("items")).isEmpty();
        mvc.perform(get(url(started)).session(otherSession.session()))
                .andExpect(
                        org.springframework.test.web.servlet.result.MockMvcResultMatchers.status()
                                .isNotFound());
        jdbc.update(
                "UPDATE camera_access_job SET expires_at=DATEADD('DAY',-2,CURRENT_TIMESTAMP) WHERE id=?",
                started.path("jobId").asLong());
        jobs.maintenance(false);
        assertThat(
                        jdbc.queryForObject(
                                "SELECT COUNT(*) FROM camera_access_import_item", Integer.class))
                .isZero();
        assertThat(jdbc.queryForObject("SELECT COUNT(*) FROM camera_channel", Integer.class))
                .isOne();
    }

    @Test
    void pageFailureAndRestartPreserveProgressWithoutInventingFailedItems() throws Exception {
        var auth = admin();
        var catalog = page(1, 0, 1, 101, true);
        var started = start(auth, catalog, null);
        var work = process(jobs.claim(started.path("jobId").asLong()), catalog);
        jobs.fail(work.id(), work.version(), "PLATFORM_API_REJECTED");
        var result = read(url(started), auth);
        assertThat(result.path("status").asText()).isEqualTo("FAILED");
        assertThat(result.path("bulk").path("failedCount").asInt()).isZero();
        assertThat(result.path("bulk").path("createdCount").asInt()).isOne();
        assertThat(result.path("diagnostic").path("reasonCode").asText())
                .isEqualTo("PLATFORM_API_REJECTED");
        var next = start(auth, catalog, null);
        var nextWork = bulk.beginPage(jobs.claim(next.path("jobId").asLong()), catalog);
        bulk.importItem(nextWork, catalog, 0);
        jobs.maintenance(true);
        assertThat(read(url(next), auth).path("status").asText()).isEqualTo("FAILED");
        assertThat(read(url(next), auth).path("bulk").path("createdCount").asInt()).isOne();
    }

    @Test
    void activeExpiryKeepsReceiptsAndIncompleteDirectoryIsNotReportedAsLimit() throws Exception {
        var auth = admin();
        var catalog = page(1, 0, 1, 101, true);
        var started = start(auth, catalog, null);
        var work = bulk.beginPage(jobs.claim(started.path("jobId").asLong()), catalog);
        bulk.importItem(work, catalog, 0);
        jdbc.update(
                "UPDATE camera_access_job SET expires_at=DATEADD('MINUTE',-1,CURRENT_TIMESTAMP) WHERE id=?",
                started.path("jobId").asLong());
        jobs.maintenance(false);
        var expired = read(url(started), auth);
        assertThat(expired.path("status").asText()).isEqualTo("EXPIRED");
        assertThat(expired.path("bulk").path("createdCount").asInt()).isOne();
        assertThat(expired.path("diagnostic").path("reasonCode").asText())
                .isEqualTo("IMPORT_TIME_LIMIT_REACHED");
        var second = start(auth, catalog, null);
        var changed =
                new CameraAccessCatalog(
                        "HIK_PLATFORM",
                        null,
                        List.of(channel(0)),
                        false,
                        List.of("DIRECTORY_INCOMPLETE"),
                        new CameraAccessCatalog.Page(1, 100, 205L, true));
        process(jobs.claim(second.path("jobId").asLong()), changed);
        var result = read(url(second), auth);
        assertThat(result.path("bulk").path("total").asLong()).isEqualTo(205);
        assertThat(result.path("status").asText()).isEqualTo("PARTIAL");
        assertThat(result.path("diagnostic").path("reasonCode").asText())
                .isEqualTo("DIRECTORY_INCOMPLETE");
    }

    @Test
    void claimRechecksRevokedActorAndChangedSourceAtCurrentBulkVersion() throws Exception {
        var auth = admin();
        var catalog = page(1, 0, 1, 1, false);
        var first = start(auth, catalog, null);
        assertThat(first.path("version").asLong()).isGreaterThan(0);
        doThrow(new CameraAdapterException("ACTOR_REVOKED"))
                .when(actors)
                .check(anyLong(), anyString());
        assertThat(jobs.claim(first.path("jobId").asLong())).isNull();
        assertThat(read(url(first), auth).path("status").asText()).isEqualTo("CANCELLED");
        assertThat(
                        jdbc.queryForObject(
                                "SELECT secret_ciphertext FROM camera_access_job WHERE id=?",
                                byte[].class,
                                first.path("jobId").asLong()))
                .isNull();
        reset(actors);
        var second = start(auth, catalog, null);
        jdbc.update(
                "UPDATE camera_source SET version=version+1 WHERE id=?",
                second.path("sourceId").asLong());
        assertThat(jobs.claim(second.path("jobId").asLong())).isNull();
        assertThat(read(url(second), auth).path("status").asText()).isEqualTo("FAILED");
        assertThat(read(url(second), auth).path("diagnostic").path("reasonCode").asText())
                .isEqualTo("SOURCE_CHANGED");
        assertThat(jdbc.queryForObject("SELECT COUNT(*) FROM camera_channel", Integer.class))
                .isZero();
    }

    @Test
    void retentionCleanupCapsCascadeAtTwentyThousandReceiptsPerPass() throws Exception {
        var auth = admin();
        var catalog = page(1, 0, 1, 1, false);
        for (int batch = 0; batch < 3; batch++) {
            var started = start(auth, catalog, null);
            long jobId = started.path("jobId").asLong();
            process(jobs.claim(jobId), catalog);
            // Seed retained historical receipts directly: this is a cleanup-volume fixture,
            // not a claim that thirty thousand upstream resources were fetched or imported.
            jdbc.update(
                    "INSERT INTO camera_access_import_item(id,job_id,external_key,page_number,item_index,status,camera_id,name,created_at) "
                            + "SELECT ?+X,?,CONCAT('history-',X),1,0,'EXISTING',?,'history',CURRENT_TIMESTAMP FROM SYSTEM_RANGE(1,9999)",
                    1_000_000L + batch * 10_000L,
                    jobId,
                    9_000_000L + batch);
            jdbc.update(
                    "UPDATE camera_access_job SET bulk_processed_count=10000,bulk_created_count=1,bulk_existing_count=9999,"
                            + "expires_at=DATEADD('DAY',-2,CURRENT_TIMESTAMP) WHERE id=?",
                    jobId);
        }
        assertThat(
                        jdbc.queryForObject(
                                "SELECT COUNT(*) FROM camera_access_import_item", Integer.class))
                .isEqualTo(30000);
        jobs.maintenance(false);
        assertThat(
                        jdbc.queryForObject(
                                "SELECT COUNT(*) FROM camera_access_import_item", Integer.class))
                .isEqualTo(10000);
        assertThat(jdbc.queryForObject("SELECT COUNT(*) FROM camera_access_job", Integer.class))
                .isOne();
        assertThat(jdbc.queryForObject("SELECT COUNT(*) FROM camera_channel", Integer.class))
                .isEqualTo(3);
        jobs.maintenance(false);
        assertThat(
                        jdbc.queryForObject(
                                "SELECT COUNT(*) FROM camera_access_import_item", Integer.class))
                .isZero();
        assertThat(jdbc.queryForObject("SELECT COUNT(*) FROM camera_channel", Integer.class))
                .isEqualTo(3);
    }

    private JsonNode discover(
            Csrf auth, CameraAccessCatalog catalog, String sourceId, String sourceVersion)
            throws Exception {
        Map<String, Object> connection =
                sourceId == null
                        ? Map.of(
                                "method",
                                "HIK_PLATFORM",
                                "host",
                                "10.0.1.5",
                                "port",
                                80,
                                "scheme",
                                "http",
                                "username",
                                "app-key",
                                "password",
                                "app-secret",
                                "networkPolicyKey",
                                "test",
                                "name",
                                "测试平台")
                        : Map.of("sourceId", sourceId, "sourceVersion", sourceVersion);
        var ticket =
                write(
                        post("/camera-access/jobs"),
                        auth,
                        Map.of("clientRequestId", requestId(), "connection", connection),
                        202);
        var work = jobs.claim(ticket.path("jobId").asLong());
        jobs.complete(work, catalog);
        return read(url(ticket), auth);
    }

    private Map<String, Object> command(Csrf auth, JsonNode discovered, String groupId)
            throws Exception {
        var command = new LinkedHashMap<String, Object>();
        command.put("version", discovered.path("version").asText());
        command.put("groupId", groupId);
        var preview = write(post(url(discovered) + "/bulk-import-preview"), auth, command, 200);
        command.put("confirmation", preview.path("confirmation").asText());
        command.put("clientRequestId", requestId());
        return command;
    }

    private JsonNode start(Csrf auth, CameraAccessCatalog catalog, String groupId)
            throws Exception {
        var discovered = discover(auth, catalog, null, null);
        return write(
                post(url(discovered) + "/bulk-import"),
                auth,
                command(auth, discovered, groupId),
                202);
    }

    private CameraAccessJobsService.Work process(
            CameraAccessJobsService.Work work, CameraAccessCatalog page) {
        work = bulk.beginPage(work, page);
        for (int i = 0; i < page.channels().size(); i++) work = bulk.importItem(work, page, i);
        return bulk.finishPage(work, page);
    }

    private static String url(JsonNode job) {
        return "/camera-access/jobs/" + job.path("jobId").asText();
    }

    private static CameraAccessCatalog.Channel channel(int n) {
        return new CameraAccessCatalog.Channel("key-" + n, "camera-" + n, List.of(), false);
    }

    private static CameraAccessCatalog page(
            int page, int from, int count, long total, boolean more) {
        return new CameraAccessCatalog(
                "HIK_PLATFORM",
                null,
                IntStream.range(from, from + count)
                        .mapToObj(CameraBulkImportIntegrationTest::channel)
                        .toList(),
                true,
                List.of(),
                new CameraAccessCatalog.Page(page, 100, total, more));
    }
}
