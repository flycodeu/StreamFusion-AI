package com.streamfusion.platform;

import static org.assertj.core.api.Assertions.assertThat;
import static org.assertj.core.api.Assertions.assertThatThrownBy;
import static org.mockito.ArgumentMatchers.any;
import static org.mockito.Mockito.*;

import com.fasterxml.jackson.databind.ObjectMapper;
import com.streamfusion.platform.audit.mapper.OperationLogMapper;
import com.streamfusion.platform.audit.pojo.entity.OperationLogEntity;
import com.streamfusion.platform.audit.service.impl.AuditServiceImpl;
import com.streamfusion.platform.auth.pojo.dto.LoginDto;
import com.streamfusion.platform.auth.pojo.dto.PasswordChangeDto;
import com.streamfusion.platform.user.pojo.entity.UserEntity;
import java.time.Clock;
import java.time.Instant;
import java.time.ZoneOffset;
import java.util.Collections;
import java.util.List;
import java.util.Map;
import org.junit.jupiter.api.Test;
import org.mockito.ArgumentCaptor;

/** 校验 Lombok 和 Jackson 共同作用时的凭证边界，避免生成方法泄露字段。 */
class PojoContractTest {
    private final ObjectMapper json = new ObjectMapper().findAndRegisterModules();

    @Test
    void passwordDtosAcceptInputButNeverSerializeOrPrintCredentials() throws Exception {
        LoginDto login =
                json.readValue(
                        "{\"username\":\"Admin01\",\"password\":\"SyntheticSecret1!\"}",
                        LoginDto.class);
        assertThat(login.getPassword()).isEqualTo("SyntheticSecret1!");
        assertThat(login.toString()).doesNotContain("SyntheticSecret1!");
        assertThat(json.writeValueAsString(login)).doesNotContain("password", "SyntheticSecret1!");

        PasswordChangeDto change =
                json.readValue(
                        "{\"currentPassword\":\"SyntheticOld1!\",\"newPassword\":\"SyntheticNew2@\"}",
                        PasswordChangeDto.class);
        assertThat(change.getCurrentPassword()).isEqualTo("SyntheticOld1!");
        assertThat(change.getNewPassword()).isEqualTo("SyntheticNew2@");
        assertThat(change.toString()).doesNotContain("SyntheticOld1!", "SyntheticNew2@");
        assertThat(json.writeValueAsString(change))
                .doesNotContain(
                        "currentPassword", "newPassword", "SyntheticOld1!", "SyntheticNew2@");
    }

    @Test
    void persistencePasswordHashIsExcludedFromGeneratedStringAndJackson() throws Exception {
        UserEntity user = new UserEntity();
        user.setId(9007199254740993L);
        user.setUsername("Admin01");
        user.setPassword("synthetic-hash-never-print");
        assertThat(user.toString()).doesNotContain("synthetic-hash-never-print");
        assertThat(json.writeValueAsString(user))
                .doesNotContain("password", "synthetic-hash-never-print");
    }

    @Test
    void auditSummaryStoresExplicitFieldsAndRejectsSecretsOrOversizedRelations() throws Exception {
        OperationLogMapper mapper = mock(OperationLogMapper.class);
        when(mapper.insert(any(OperationLogEntity.class))).thenReturn(1);
        var audit =
                new AuditServiceImpl(
                        mapper,
                        json,
                        Clock.fixed(Instant.parse("2026-09-26T00:00:00Z"), ZoneOffset.UTC));
        audit.record(
                1L,
                "ROLE",
                2L,
                "ROLE_MENUS_UPDATE",
                "SUCCESS",
                null,
                null,
                Map.of(
                        "name",
                        "业务管理员",
                        "beforeMenuIds",
                        List.of(1002L),
                        "afterMenuIds",
                        List.of("1002", "1004")));
        ArgumentCaptor<OperationLogEntity> entry =
                ArgumentCaptor.forClass(OperationLogEntity.class);
        verify(mapper).insert(entry.capture());
        var changes = json.readTree(entry.getValue().getChanges());
        assertThat(changes.path("name").asText()).isEqualTo("业务管理员");
        assertThat(changes.path("beforeMenuIds").get(0).asText()).isEqualTo("1002");
        assertThat(changes.path("afterMenuIds").size()).isEqualTo(2);
        assertThat(entry.getValue().getCreatedAt().toString()).isEqualTo("2026-09-26T08:00");
        clearInvocations(mapper);

        for (Map<String, ?> invalid :
                List.of(
                        Map.of("password", "never-persist"),
                        Map.of("name", "x".repeat(129)),
                        Map.of("menuIds", Collections.nCopies(1001, "1002")),
                        Map.of("menuIds", List.of("not-an-id")))) {
            assertThatThrownBy(
                            () ->
                                    audit.record(
                                            1L, "ROLE", 2L, "UPDATE", "SUCCESS", null, null,
                                            invalid))
                    .isInstanceOf(IllegalArgumentException.class);
        }
        verifyNoInteractions(mapper);
    }
}
