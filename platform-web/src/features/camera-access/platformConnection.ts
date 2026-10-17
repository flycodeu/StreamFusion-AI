import type { SourceCreate, SourcePurpose } from '../../api/camera-sources/types'
import type { ConnectionDraft } from './form'

interface PlatformConnectionConfig {
  defaultName: string
  credentialPurpose: SourcePurpose
  basePath: string
  usernameLabel: string
  passwordLabel: string
  usernamePlaceholder: string
  passwordPlaceholder: string
  missingCredentialsMessage: string
}

export const hikPlatformConnection: Readonly<PlatformConnectionConfig> = {
  defaultName: '海康平台',
  credentialPurpose: 'PLATFORM_HTTP',
  basePath: '/artemis',
  usernameLabel: 'AppKey',
  passwordLabel: 'AppSecret',
  usernamePlaceholder: '设备或平台提供的账号',
  passwordPlaceholder: '不会回显已保存密码',
  missingCredentialsMessage: '请填写平台提供的 AppKey 和 AppSecret。',
}

const platformConnections = new Map([['HIK_PLATFORM', hikPlatformConnection]])

export function getPlatformConnection(method: string | null | undefined) {
  return method ? platformConnections.get(method) : undefined
}

export function platformSourceInput(
  draft: ConnectionDraft,
  config: Readonly<PlatformConnectionConfig>,
): Omit<SourceCreate, 'clientRequestId'> {
  return {
    name: draft.name.trim() || config.defaultName,
    adapterType: draft.method,
    connectionCategory: 'PLATFORM',
    networkPolicyKey: draft.networkPolicyKey || undefined,
    enabled: true,
    credentials: [
      {
        purpose: config.credentialPurpose,
        username: { action: 'REPLACE', value: draft.username },
        password: { action: 'REPLACE', value: draft.password },
      },
    ],
    endpoints: [
      {
        purpose: config.credentialPurpose,
        scheme: draft.scheme,
        host: draft.host.trim(),
        port: draft.port,
        basePath: config.basePath,
        authMode: 'DRIVER_NEGOTIATED',
        credentialPurpose: config.credentialPurpose,
        tlsPolicy: 'SYSTEM_CA',
      },
    ],
  }
}
