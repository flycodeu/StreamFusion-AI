export interface ObservationAttempt {
  code?: string;
  at?: string;
  traceId?: string;
}
export interface ObservationBatch {
  id: string;
  status: string;
  traceId: string;
  createdAt: string;
  items: {
    objectId: string;
    hostId: string;
    name: string;
    status: string;
    errorCode: string;
    observedAt: string;
  }[];
}
export const observationPending = (batch?: ObservationBatch) =>
  !!batch && ["QUEUED", "RUNNING"].includes(batch.status);
