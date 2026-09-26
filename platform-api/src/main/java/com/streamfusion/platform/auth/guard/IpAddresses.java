package com.streamfusion.platform.auth.guard;

import java.net.InetAddress;
import java.net.UnknownHostException;

/** Literal-only IP normalization; never perform hostname DNS lookups. */
public final class IpAddresses {
    private IpAddresses() {}

    public static String canonical(String value) {
        if (value == null || value.length() > 45 || !value.matches("[0-9A-Fa-f:.]+"))
            throw new IllegalArgumentException("Invalid IP address");
        try {
            if (!value.contains(":")) {
                String[] parts = value.split("\\.", -1);
                if (parts.length != 4) throw new IllegalArgumentException("Invalid IP address");
                byte[] bytes = new byte[4];
                for (int i = 0; i < 4; i++) {
                    if (!parts[i].matches("0|[1-9][0-9]{0,2}"))
                        throw new IllegalArgumentException("Invalid IP address");
                    int number = Integer.parseInt(parts[i]);
                    if (number > 255) throw new IllegalArgumentException("Invalid IP address");
                    bytes[i] = (byte) number;
                }
                return InetAddress.getByAddress(bytes).getHostAddress();
            }
            return InetAddress.getByName(value).getHostAddress();
        } catch (UnknownHostException ex) {
            throw new IllegalArgumentException("Invalid IP address");
        }
    }
}
