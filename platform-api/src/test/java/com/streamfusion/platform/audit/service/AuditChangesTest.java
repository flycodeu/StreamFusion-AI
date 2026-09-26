package com.streamfusion.platform.audit.service;

import static org.assertj.core.api.Assertions.assertThat;
import static org.assertj.core.api.Assertions.assertThatThrownBy;
import static org.mockito.ArgumentMatchers.anyList;
import static org.mockito.ArgumentMatchers.eq;
import static org.mockito.Mockito.*;

import com.fasterxml.jackson.databind.ObjectMapper;
import com.streamfusion.platform.audit.mapper.AuditReferenceMapper;
import com.streamfusion.platform.audit.pojo.dto.AuditSubject;
import java.util.ArrayList;
import java.util.List;
import java.util.Map;
import java.util.stream.IntStream;
import org.junit.jupiter.api.Test;

class AuditChangesTest {
    private final AuditChanges changes = new AuditChanges(new ObjectMapper());

    @Test
    void rejectsSecretsOversizedInputAndForgedSnapshotObjects() {
        assertThatThrownBy(() -> changes.select(Map.of("_actor", Map.of("name", "injected"))))
                .isInstanceOf(IllegalArgumentException.class);
        assertThatThrownBy(() -> changes.select(Map.of("password", "secret")))
                .isInstanceOf(IllegalArgumentException.class);
        assertThatThrownBy(() -> changes.select(Map.of("roleIds", List.of("9223372036854775808"))))
                .isInstanceOf(IllegalArgumentException.class);
        assertThatThrownBy(() -> changes.select(Map.of("name", "𠀀".repeat(129))))
                .isInstanceOf(IllegalArgumentException.class);
        String invalid =
                """
                {"roleIds":["1"],"_relations":{"ROLE:1":{"id":"1","type":"ROLE","name":"safe","code":null,"password":"secret"}},
                 "_actor":{"id":"1","type":"USER","name":"safe","code":"u","token":"secret"},"cookie":"secret"}
                """;
        var parsed = changes.read(invalid);
        assertThat(parsed.fields()).containsOnlyKeys("roleIds");
        assertThat(parsed.references()).isEmpty();
        assertThat(parsed.actor()).isNull();
        assertThat(changes.read("x".repeat(AuditChanges.MAX_JSON_LENGTH + 1)).fields()).isEmpty();
    }

    @Test
    void fullBoundedRelationsSurviveTheFormerReadLimitAndReuseSnapshotIds() throws Exception {
        AuditReferenceMapper mapper = mock(AuditReferenceMapper.class);
        when(mapper.find(eq("ROLE"), anyList()))
                .thenAnswer(
                        invocation -> {
                            List<Long> ids = invocation.getArgument(1);
                            return ids.stream()
                                    .map(
                                            id -> {
                                                AuditSubject value = new AuditSubject();
                                                value.setId(id);
                                                value.setName("角".repeat(64));
                                                value.setCode("R".repeat(60) + (id % 1000));
                                                return value;
                                            })
                                    .toList();
                        });
        AuditReferences references = new AuditReferences(mapper);
        List<String> before = IntStream.rangeClosed(1, 1000).mapToObj(String::valueOf).toList();
        List<String> after = IntStream.rangeClosed(1001, 2000).mapToObj(String::valueOf).toList();
        String text =
                references.capture(
                        null,
                        "ROLE",
                        null,
                        Map.of("beforeRoleIds", before, "afterRoleIds", after, "roleIds", after),
                        changes);
        assertThat(text.length()).isGreaterThan(262144).isLessThan(AuditChanges.MAX_JSON_LENGTH);
        var parsed = changes.read(text);
        assertThat(parsed.references()).hasSize(2000);
        assertThat(parsed.fields().get("afterRoleIds")).isEqualTo(after);
        assertThat(parsed.references().get("ROLE:2000").name()).isEqualTo("角".repeat(64));
        verify(mapper, times(2)).find(eq("ROLE"), anyList());
        assertThat(new ObjectMapper().readTree(text).path("_relations").size()).isEqualTo(2000);
    }

    @Test
    void batchesAndDeduplicatesReferenceLookupsByType() {
        AuditReferenceMapper mapper = mock(AuditReferenceMapper.class);
        List<AuditReferences.Request> ids = new ArrayList<>();
        for (int id = 1; id <= 1001; id++) {
            ids.add(new AuditReferences.Request("ROLE", Integer.toString(id)));
            ids.add(new AuditReferences.Request("ROLE", Integer.toString(id)));
        }
        ids.add(new AuditReferences.Request("UNKNOWN", "1"));
        new AuditReferences(mapper).current(ids);
        verify(mapper, times(2)).find(eq("ROLE"), anyList());
        verifyNoMoreInteractions(mapper);
    }
}
