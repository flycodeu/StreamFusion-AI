import { request } from '../client'
import { boolean, integer, object, optionalString, string, list } from '../parse'
import { ApiRequestError } from '../../lib/http/error'
import { previewReason } from '../../features/preview/model'
import type { ApiResult } from '../../lib/http/types'

function resource(value: unknown) {
  const row = object(value)
  return {
    resourceId: string(row.resourceId),
    bootId: string(row.bootId),
    generation: integer(row.generation),
    state: string(row.state),
    expiresAt: integer(row.expiresAt),
    reasonCode: optionalString(row.reasonCode),
  }
}
export function parsePreviewNode(value: unknown) {
  const row = object(value),
    observed = row.observation == null ? null : object(row.observation)
  return {
    mode: string(row.mode),
    version: integer(row.version),
    nodeId: optionalString(row.nodeId),
    name: optionalString(row.name),
    controlUri: optionalString(row.controlUri),
    mediaAddress: optionalString(row.mediaAddress),
    desiredConfigVersion: optionalString(row.desiredConfigVersion),
    state: string(row.state),
    reasonCode: optionalString(row.reasonCode),
    observedAt: optionalString(row.observedAt),
    retainedViewers: integer(row.retainedViewers),
    registrationAvailable: boolean(row.registrationAvailable),
    observation:
      observed == null
        ? null
        : {
            nodeId: string(observed.nodeId),
            bootId: string(observed.bootId),
            version: string(observed.version),
            configVersion: string(observed.configVersion),
            state: string(observed.state),
            maxSessions: integer(observed.maxSessions),
            maxTranscodes: integer(observed.maxTranscodes),
            occupiedTranscodes: integer(observed.occupiedTranscodes),
            observedAt: integer(observed.observedAt),
            resources: list(observed.resources, resource),
          },
  }
}
export type PreviewNodeView = ReturnType<typeof parsePreviewNode>
export function parseNodeAction(value: unknown) {
  const row = object(value)
  return {
    applied: boolean(row.applied),
    reasonCode: optionalString(row.reasonCode),
    node: parsePreviewNode(row.node),
  }
}
function applied(result: ApiResult<ReturnType<typeof parseNodeAction>>) {
  if (!result.data.applied) {
    const code = /^[A-Z][A-Z0-9_]{0,79}$/.test(result.data.reasonCode ?? '')
      ? result.data.reasonCode!
      : 'NODE_UNAVAILABLE'
    throw new ApiRequestError(code, previewReason(code), result.meta)
  }
  return result.data.node
}
export async function getPreviewNode(signal?: AbortSignal) {
  return (
    await request({
      path: '/preview-node',
      method: 'GET',
      successStatus: 200,
      signal,
      decode: parsePreviewNode,
    })
  ).data
}
export async function previewNodeAction(
  action: 'check' | 'register' | 'reconcile',
  signal?: AbortSignal,
) {
  if (action === 'check')
    return (
      await request({
        path: '/preview-node/check',
        method: 'POST',
        successStatus: 200,
        timeoutMs: 10000,
        signal,
        decode: parsePreviewNode,
      })
    ).data
  return applied(
    await request({
      path: `/preview-node/${action}`,
      method: 'POST',
      successStatus: 200,
      timeoutMs: 20000,
      signal,
      decode: parseNodeAction,
    }),
  )
}
export async function selectPreviewNode(mode: string, version: number) {
  return applied(
    await request({
      path: '/preview-node/mode',
      method: 'PUT',
      body: { mode, version },
      successStatus: 200,
      timeoutMs: 10000,
      decode: parseNodeAction,
    }),
  )
}

export function parseBudget(value: unknown) {
  const row = object(value)
  return {
    maxSources: integer(row.maxSources),
    maxViewers: integer(row.maxViewers),
    maxTranscodes: integer(row.maxTranscodes),
    maxStarting: integer(row.maxStarting),
    ingressKbps: integer(row.ingressKbps),
    egressKbps: integer(row.egressKbps),
    sourceUpperKbps: integer(row.sourceUpperKbps),
    outputUpperKbps: integer(row.outputUpperKbps),
  }
}
function loadPercent(value: unknown): number {
  if (typeof value !== 'number' || !Number.isFinite(value) || value < 0 || value > 100)
    throw new Error('loadPercent')
  return value
}

function parseZlmLoad(value: unknown) {
  const row = object(value)
  const networkThreadCount = integer(row.networkThreadCount)
  const averageNetworkThreadLoadPercent = loadPercent(row.averageNetworkThreadLoadPercent)
  const peakNetworkThreadLoadPercent = loadPercent(row.peakNetworkThreadLoadPercent)
  const observedAt = string(row.observedAt)
  if (networkThreadCount < 1 || !Number.isFinite(Date.parse(observedAt))) throw new Error('zlmLoad')
  return {
    networkThreadCount,
    averageNetworkThreadLoadPercent,
    peakNetworkThreadLoadPercent,
    observedAt,
  }
}

export function parseCluster(value: unknown) {
  const root = object(value)
  return {
    nodes: list(root.nodes, (value) => {
      const row = object(value),
        used = object(row.usage)
      return {
        nodeId: string(row.nodeId),
        kind: optionalString(row.kind),
        name: string(row.name),
        mode: string(row.mode),
        version: integer(row.version),
        priority: integer(row.priority),
        budget: parseBudget(row.budget),
        usage: {
          sources: integer(used.sources),
          viewers: integer(used.viewers),
          transcodes: integer(used.transcodes),
          starting: integer(used.starting),
          ingressKbps: integer(used.ingressKbps),
          egressKbps: integer(used.egressKbps),
        },
        state: string(row.state),
        reasonCode: optionalString(row.reasonCode),
        observedAt: optionalString(row.observedAt),
        controlUri: optionalString(row.controlUri),
        zlmApiAddress: optionalString(row.zlmApiAddress),
        certificateSha256: optionalString(row.certificateSha256),
        mediaAddress: optionalString(row.mediaAddress),
        desiredConfigVersion: string(row.desiredConfigVersion),
        observation: row.observation == null ? null : object(row.observation),
        protection: row.protection == null ? null : object(row.protection),
        zlmLoad: row.zlmLoad == null ? null : parseZlmLoad(row.zlmLoad),
      }
    }),
    registrationOptions: list(root.registrationOptions, (value) => {
      const row = object(value)
      return { nodeId: string(row.nodeId), name: string(row.name) }
    }),
    directRegistrationAvailable:
      root.directRegistrationAvailable == null ? false : boolean(root.directRegistrationAvailable),
  }
}
export type PreviewCluster = ReturnType<typeof parseCluster>
export type PreviewNodeStatus = PreviewCluster['nodes'][number]
export type PreviewBudget = PreviewNodeStatus['budget']
export type PreviewNodeConnectionInput = {
  nodeId: string
  name: string
  controlUri: string
  certificateSha256: string
  mediaAddress: string
  version: number | null
}

export type DirectZlmInput = {
  name: string
  apiHost: string
  apiPort: number
  mediaAddress: string
  secret: string
  priority: number
  budget: PreviewBudget
}

export function parseDirectZlmInstances(value: unknown) {
  const root = object(value)
  return {
    registrationAvailable: boolean(root.registrationAvailable),
    items: list(root.items, (value) => {
      const row = object(value),
        used = object(row.usage)
      return {
        nodeId: string(row.nodeId),
        name: string(row.name),
        apiHost: string(row.apiHost),
        apiPort: integer(row.apiPort),
        zlmVersion: optionalString(row.zlmVersion),
        mediaAddress: string(row.mediaAddress),
        mode: string(row.mode),
        version: integer(row.version),
        priority: integer(row.priority),
        budget: parseBudget(row.budget),
        usage: {
          sources: integer(used.sources),
          viewers: integer(used.viewers),
          transcodes: integer(used.transcodes),
          starting: integer(used.starting),
          ingressKbps: integer(used.ingressKbps),
          egressKbps: integer(used.egressKbps),
        },
        state: string(row.state),
        reasonCode: optionalString(row.reasonCode),
        observedAt: optionalString(row.observedAt),
      }
    }),
  }
}
export type DirectZlmInstances = ReturnType<typeof parseDirectZlmInstances>
export type DirectZlmInstance = DirectZlmInstances['items'][number]

function policyReceipt(value: unknown) {
  const row = object(value)
  return { applied: boolean(row.applied), reasonCode: optionalString(row.reasonCode) }
}
function requireApplied(result: ApiResult<ReturnType<typeof policyReceipt>>) {
  if (!result.data.applied) {
    const code = result.data.reasonCode ?? 'NODE_UNAVAILABLE'
    throw new ApiRequestError(code, previewReason(code), result.meta)
  }
}
export async function getDirectZlmInstances(signal?: AbortSignal) {
  return (
    await request({
      path: '/preview-node/zlm-instances',
      method: 'GET',
      successStatus: 200,
      signal,
      decode: parseDirectZlmInstances,
    })
  ).data
}
export async function createDirectZlmInstance(input: DirectZlmInput) {
  requireApplied(
    await request({
      path: '/preview-node/zlm-instances',
      method: 'POST',
      body: input,
      successStatus: 200,
      timeoutMs: 20000,
      decode: policyReceipt,
    }),
  )
}
export async function saveDirectZlmInstance(
  instance: DirectZlmInstance,
  changes: Partial<Pick<DirectZlmInstance, 'mode' | 'priority' | 'budget'>>,
) {
  requireApplied(
    await request({
      path: `/preview-node/zlm-instances/${encodeURIComponent(instance.nodeId)}`,
      method: 'PUT',
      body: {
        mode: changes.mode ?? instance.mode,
        priority: changes.priority ?? instance.priority,
        budget: changes.budget ?? instance.budget,
        version: instance.version,
      },
      successStatus: 200,
      timeoutMs: 10000,
      decode: policyReceipt,
    }),
  )
}
export async function rotateDirectZlmSecret(instance: DirectZlmInstance, secret: string) {
  requireApplied(
    await request({
      path: `/preview-node/zlm-instances/${encodeURIComponent(instance.nodeId)}/credential`,
      method: 'PUT',
      body: { secret, version: instance.version },
      successStatus: 200,
      timeoutMs: 20000,
      decode: policyReceipt,
    }),
  )
}
export async function deleteDirectZlmInstance(instance: DirectZlmInstance) {
  requireApplied(
    await request({
      path: `/preview-node/zlm-instances/${encodeURIComponent(instance.nodeId)}?version=${instance.version}`,
      method: 'DELETE',
      successStatus: 200,
      timeoutMs: 10000,
      decode: policyReceipt,
    }),
  )
}

export function parseOpsPreviewInstances(value: unknown) {
  const root = object(value)
  return {
    available: boolean(root.available),
    reasonCode: optionalString(root.reasonCode),
    items: list(root.items, (value) => {
      const row = object(value)
      return {
        instanceId: string(row.instanceId),
        name: string(row.name),
        targetName: string(row.targetName),
        targetAddress: string(row.targetAddress),
        installRoot: string(row.installRoot),
        releaseId: string(row.releaseId),
        nodeId: string(row.nodeId),
        controlUri: string(row.controlUri),
        mediaAddress: string(row.mediaAddress),
        maxSources: integer(row.maxSources),
        maxViewers: integer(row.maxViewers),
        maxTranscodes: integer(row.maxTranscodes),
      }
    }),
  }
}
export type OpsPreviewInstances = ReturnType<typeof parseOpsPreviewInstances>

export function parseOpsZlmInstances(value: unknown) {
  const root = object(value)
  return {
    available: boolean(root.available),
    reasonCode: optionalString(root.reasonCode),
    items: list(root.items, (value) => {
      const row = object(value)
      return {
        instanceId: string(row.instanceId),
        name: string(row.name),
        targetName: string(row.targetName),
        targetAddress: string(row.targetAddress),
        installRoot: string(row.installRoot),
        releaseId: string(row.releaseId),
        apiUrl: string(row.apiUrl),
        mediaAddress: string(row.mediaAddress),
        apiPort: integer(row.apiPort),
        rtcPort: integer(row.rtcPort),
        maxSources: integer(row.maxSources),
        maxViewers: integer(row.maxViewers),
        maxTranscodes: integer(row.maxTranscodes),
      }
    }),
  }
}
export type OpsZlmInstances = ReturnType<typeof parseOpsZlmInstances>

export async function getOpsZlmInstances(signal?: AbortSignal) {
  return (
    await request({
      path: '/preview-node/ops-zlm-instances',
      method: 'GET',
      successStatus: 200,
      signal,
      decode: parseOpsZlmInstances,
    })
  ).data
}

export async function importOpsZlmInstance(instanceId: string, releaseId: string) {
  requireApplied(
    await request({
      path: '/preview-node/ops-zlm-import',
      method: 'POST',
      body: { instanceId, releaseId },
      successStatus: 200,
      timeoutMs: 30000,
      decode: policyReceipt,
    }),
  )
}

export async function getOpsPreviewInstances(signal?: AbortSignal) {
  return (
    await request({
      path: '/preview-node/ops-instances',
      method: 'GET',
      successStatus: 200,
      signal,
      decode: parseOpsPreviewInstances,
    })
  ).data
}

export async function importOpsPreviewInstance(instanceId: string, releaseId: string) {
  return applied(
    await request({
      path: '/preview-node/ops-import',
      method: 'POST',
      body: { instanceId, releaseId },
      successStatus: 200,
      timeoutMs: 20000,
      decode: parseNodeAction,
    }),
  )
}

export async function configurePreviewNode(input: PreviewNodeConnectionInput) {
  return applied(
    await request({
      path: '/preview-node/connections',
      method: 'POST',
      body: input,
      successStatus: 200,
      timeoutMs: 10000,
      decode: parseNodeAction,
    }),
  )
}
export async function getPreviewNodes(signal?: AbortSignal) {
  return (
    await request({
      path: '/preview-node/nodes',
      method: 'GET',
      successStatus: 200,
      signal,
      decode: parseCluster,
    })
  ).data
}
export async function registerPreviewNode(nodeId: string) {
  return applied(
    await request({
      path: `/preview-node/register?nodeId=${encodeURIComponent(nodeId)}`,
      method: 'POST',
      successStatus: 200,
      timeoutMs: 10000,
      decode: parseNodeAction,
    }),
  )
}
export async function savePreviewNode(node: PreviewNodeStatus) {
  const result = await request({
    path: `/preview-node/nodes/${encodeURIComponent(node.nodeId)}`,
    method: 'PUT',
    body: { mode: node.mode, version: node.version, priority: node.priority, budget: node.budget },
    successStatus: 200,
    timeoutMs: 10000,
    decode: (value) => {
      const row = object(value)
      return { applied: boolean(row.applied), reasonCode: optionalString(row.reasonCode) }
    },
  })
  if (!result.data.applied) {
    const code = result.data.reasonCode ?? 'NODE_CONFIGURATION_CHANGED'
    throw new ApiRequestError(code, previewReason(code), result.meta)
  }
}

export function parseMaintenance(value: unknown) {
  const row = object(value)
  return {
    state: string(row.state),
    nodeId: string(row.nodeId),
    permitId: optionalString(row.permitId),
    bootId: optionalString(row.bootId),
    configVersion: optionalString(row.configVersion),
    jobId: optionalString(row.jobId),
    expiresAt: integer(row.expiresAt),
  }
}
export type PreviewMaintenance = ReturnType<typeof parseMaintenance>
export async function previewMaintenance(
  node: PreviewNodeStatus,
  action?: 'PREPARE' | 'RELEASE',
  permitId?: string,
) {
  const result = await request({
    path: `/preview-node/nodes/${encodeURIComponent(node.nodeId)}/maintenance`,
    method: action ? 'POST' : 'GET',
    body: action ? { action, permitId, version: node.version } : undefined,
    successStatus: 200,
    timeoutMs: 20000,
    decode: (value) => {
      const row = object(value)
      return {
        applied: boolean(row.applied),
        reasonCode: optionalString(row.reasonCode),
        maintenance: row.maintenance == null ? null : parseMaintenance(row.maintenance),
      }
    },
  })
  if (!result.data.applied || !result.data.maintenance) {
    const code = result.data.reasonCode ?? 'NODE_UNAVAILABLE'
    throw new ApiRequestError(code, previewReason(code), result.meta)
  }
  return result.data.maintenance
}
