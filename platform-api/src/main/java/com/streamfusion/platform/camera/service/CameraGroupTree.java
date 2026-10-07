package com.streamfusion.platform.camera.service;

import com.streamfusion.platform.camera.pojo.entity.CameraGroupEntity;
import com.streamfusion.platform.common.exception.*;
import java.util.*;

/** Pure operations on the bounded group snapshot; never enumerates camera IDs. */
public final class CameraGroupTree {
    public static final int MAX_NODES = 1000;
    public static final int MAX_DEPTH = 16;
    private final Map<Long, CameraGroupEntity> nodes = new LinkedHashMap<>();

    public CameraGroupTree(List<CameraGroupEntity> rows) {
        if (rows.size() > MAX_NODES) throw BusinessException.error(ErrorCode.CONFLICT);
        for (var row : rows) nodes.put(row.getId(), row);
    }

    public List<CameraGroupEntity> all() {
        return List.copyOf(nodes.values());
    }

    public CameraGroupEntity require(long id) {
        var row = nodes.get(id);
        if (row == null) throw BusinessException.error(ErrorCode.NOT_FOUND);
        return row;
    }

    public List<Long> ancestors(Long id) {
        List<Long> result = new ArrayList<>();
        Set<Long> seen = new HashSet<>();
        while (id != null) {
            if (!seen.add(id) || result.size() >= MAX_DEPTH)
                throw BusinessException.error(ErrorCode.CONFLICT);
            result.add(id);
            id = require(id).getParentId();
        }
        return List.copyOf(result);
    }

    public List<Long> descendants(Collection<Long> roots) {
        Set<Long> found = new LinkedHashSet<>(roots);
        boolean changed = true;
        while (changed) {
            changed = false;
            for (var row : nodes.values())
                if (found.contains(row.getParentId()) && found.add(row.getId())) changed = true;
        }
        return found.stream().filter(nodes::containsKey).sorted().toList();
    }

    public String path(Long id) {
        if (id == null) return "";
        var ids = new ArrayList<>(ancestors(id));
        Collections.reverse(ids);
        return String.join(" / ", ids.stream().map(i -> require(i).getName()).toList());
    }

    public void validateMove(Long id, Long parentId) {
        List<Long> parents = ancestors(parentId);
        if (id != null && parents.contains(id)) throw BusinessException.error(ErrorCode.CONFLICT);
        int subtreeDepth = 1;
        if (id != null)
            for (long child : descendants(List.of(id))) {
                List<Long> chain = ancestors(child);
                subtreeDepth = Math.max(subtreeDepth, chain.indexOf(id) + 1);
            }
        if (parents.size() + subtreeDepth > MAX_DEPTH)
            throw BusinessException.error(ErrorCode.CONFLICT);
    }
}
