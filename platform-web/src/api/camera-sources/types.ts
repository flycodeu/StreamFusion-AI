import { boolean, id, integer, list, object, optionalString, string } from '../parse'
import type { SecretWrite } from '../camera/types'
export type SourcePurpose = 'RTSP' | 'ONVIF' | 'VENDOR_HTTP' | 'PLATFORM_HTTP' | 'DEVICE_HTTP'
export interface SourceEndpoint {
  purpose: SourcePurpose
  scheme: 'rtsp' | 'http' | 'https'
  host: string
  port: number
  basePath: string
  authMode: 'NONE' | 'DRIVER_NEGOTIATED'
  credentialPurpose: SourcePurpose | null
  tlsPolicy: 'SYSTEM_CA'
}
export interface Source {
  sourceId: string
  name: string
  adapterType: string | null
  connectionCategory: string | null
  vendorHint: string | null
  networkPolicyKey: string | null
  rtspPort: number | null
  endpointEditable: boolean
  enabled: boolean
  remark: string | null
  version: string
  createdAt: string
  updatedAt: string
  endpoints: SourceEndpoint[]
  credentials: { purpose: string; configured: boolean; usernameMasked: string }[]
}
export interface SourceOptions {
  adapterTypes: string[]
  networkPolicies: { key: string; name: string }[]
  credentialPurposes: string[]
  ready: boolean
}
export interface SourceCredential {
  purpose: SourcePurpose
  username: SecretWrite
  password: SecretWrite
}
export interface SourceUpdate {
  version: string
  adapterType?: string | null
  vendorHint?: string | null
  name?: string
  networkPolicyKey?: string
  rtspPort?: number
  enabled?: boolean
  remark?: string | null
  endpointsUpsert?: SourceEndpoint[]
  credentialsUpsert?: SourceCredential[]
  credentialsRemove?: string[]
}
export function parseSource(value: unknown): Source {
  const row = object(value)
  return {
    sourceId: id(row.sourceId),
    name: string(row.name),
    adapterType: optionalString(row.adapterType),
    connectionCategory: optionalString(row.connectionCategory),
    vendorHint: optionalString(row.vendorHint),
    networkPolicyKey: optionalString(row.networkPolicyKey),
    rtspPort: row.rtspPort == null ? null : integer(row.rtspPort),
    endpointEditable: boolean(row.endpointEditable),
    enabled: boolean(row.enabled),
    remark: optionalString(row.remark),
    version: id(row.version),
    createdAt: string(row.createdAt),
    updatedAt: string(row.updatedAt),
    endpoints: list(row.endpoints, (value) => {
      const endpoint = object(value)
      if (
        !['RTSP', 'ONVIF', 'VENDOR_HTTP', 'PLATFORM_HTTP', 'DEVICE_HTTP'].includes(
          string(endpoint.purpose),
        ) ||
        !['rtsp', 'http', 'https'].includes(string(endpoint.scheme)) ||
        !['NONE', 'DRIVER_NEGOTIATED'].includes(string(endpoint.authMode))
      )
        throw new Error('source endpoint')
      return {
        purpose: endpoint.purpose as SourcePurpose,
        scheme: endpoint.scheme as SourceEndpoint['scheme'],
        host: string(endpoint.host),
        port: integer(endpoint.port),
        basePath: optionalString(endpoint.basePath) ?? '',
        authMode: endpoint.authMode as SourceEndpoint['authMode'],
        credentialPurpose:
          endpoint.credentialPurpose == null
            ? null
            : (string(endpoint.credentialPurpose) as SourcePurpose),
        tlsPolicy: 'SYSTEM_CA',
      }
    }),
    credentials: list(row.credentials, (value) => {
      const credential = object(value)
      return {
        purpose: string(credential.purpose),
        configured: boolean(credential.configured),
        usernameMasked: string(credential.usernameMasked),
      }
    }),
  }
}
export function parseSourceOptions(value: unknown): SourceOptions {
  const row = object(value)
  return {
    adapterTypes: list(row.adapterTypes, string),
    credentialPurposes: list(row.credentialPurposes, string),
    ready: boolean(row.ready),
    networkPolicies: list(row.networkPolicies, (value) => {
      const policy = object(value)
      return { key: string(policy.key), name: string(policy.name) }
    }),
  }
}
