package com.streamfusion.platform.camera.access.service;

import com.streamfusion.platform.camera.access.adapter.CameraHttpTransport;
import com.streamfusion.platform.camera.access.pojo.CameraScanRequest;
import com.streamfusion.platform.camera.service.CameraSourceRules;
import java.util.ArrayList;
import java.util.HashSet;
import java.util.List;
import java.util.concurrent.TimeUnit;
import lombok.RequiredArgsConstructor;
import org.springframework.stereotype.Component;

/** Uses the existing access worker and policy transport; candidates are never camera identities. */
@Component
@RequiredArgsConstructor
public class CameraNetworkScan {
    private final CameraSourceRules rules;
    private final CameraHttpTransport transport;

    public record Plan(String networkPolicyKey, List<String> hosts, List<Integer> ports) {}

    public record Host(
            String candidateId, String host, List<Integer> openPorts, String identityConfidence) {}

    public record Result(
            List<Host> hosts, int scannedTargets, int totalTargets, boolean complete) {}

    public Plan prepare(CameraScanRequest input) {
        if (input == null
                || input.ports() == null
                || input.ports().isEmpty()
                || input.ports().size() > 4
                || new HashSet<>(input.ports()).size() != input.ports().size())
            throw CameraSourceRules.invalid();
        rules.policy(input.networkPolicyKey());
        for (Integer port : input.ports()) {
            if (port == null) throw CameraSourceRules.invalid();
            CameraSourceRules.port(port);
        }
        long start, end;
        if (input.cidr() != null) {
            if (input.startAddress() != null || input.endAddress() != null)
                throw CameraSourceRules.invalid();
            String[] parts = input.cidr().split("/", -1);
            if (parts.length != 2 || !parts[1].matches("(?:[0-9]|[12][0-9]|3[0-2])"))
                throw CameraSourceRules.invalid();
            int prefix = Integer.parseInt(parts[1]);
            if (prefix < 25) throw CameraSourceRules.invalid();
            long mask = (0xffffffffL << (32 - prefix)) & 0xffffffffL;
            start = ipv4(parts[0]) & mask;
            end = start | (~mask & 0xffffffffL);
        } else {
            start = ipv4(input.startAddress());
            end = ipv4(input.endAddress());
        }
        if (end < start || end - start >= 128) throw CameraSourceRules.invalid();
        var hosts = new ArrayList<String>();
        for (long value = start; value <= end; value++) {
            String host = address(value);
            rules.host(host, input.networkPolicyKey());
            hosts.add(host);
        }
        return new Plan(input.networkPolicyKey(), List.copyOf(hosts), List.copyOf(input.ports()));
    }

    public Result execute(Plan plan, Runnable authorizationCheck) {
        long deadline = System.nanoTime() + TimeUnit.SECONDS.toNanos(60);
        var found = new ArrayList<Host>();
        int completed = 0;
        for (int index = 0; index < plan.hosts().size(); index++) {
            if (System.nanoTime() >= deadline) break;
            String host = plan.hosts().get(index);
            var open = new ArrayList<Integer>();
            boolean complete = true;
            for (int port : plan.ports()) {
                if (System.nanoTime() >= deadline) {
                    complete = false;
                    break;
                }
                if (transport.tcpOpen(
                        host, port, plan.networkPolicyKey(), authorizationCheck, deadline))
                    open.add(port);
            }
            if (!open.isEmpty())
                found.add(new Host("h" + index, host, List.copyOf(open), "PORT_OPEN_ONLY"));
            if (!complete) break;
            completed++;
        }
        authorizationCheck.run();
        return new Result(
                List.copyOf(found),
                completed,
                plan.hosts().size(),
                completed == plan.hosts().size());
    }

    private static long ipv4(String text) {
        if (text == null) throw CameraSourceRules.invalid();
        String[] parts = text.split("\\.", -1);
        if (parts.length != 4) throw CameraSourceRules.invalid();
        long value = 0;
        for (String part : parts) {
            if (!part.matches("0|[1-9][0-9]{0,2}")) throw CameraSourceRules.invalid();
            int octet = Integer.parseInt(part);
            if (octet > 255) throw CameraSourceRules.invalid();
            value = (value << 8) | octet;
        }
        return value;
    }

    private static String address(long value) {
        return ((value >>> 24) & 255)
                + "."
                + ((value >>> 16) & 255)
                + "."
                + ((value >>> 8) & 255)
                + "."
                + (value & 255);
    }
}
