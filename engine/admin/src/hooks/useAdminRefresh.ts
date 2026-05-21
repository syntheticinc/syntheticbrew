import { useEffect } from 'react';

export const ADMIN_CHANGED_EVENT = 'syntheticbrew:admin-changed';

export interface AdminChangedDetail {
  tool: string;
}

export function dispatchAdminChanged(tool: string) {
  window.dispatchEvent(
    new CustomEvent<AdminChangedDetail>(ADMIN_CHANGED_EVENT, { detail: { tool } }),
  );
}

export function useAdminRefresh(refetch: () => void) {
  useEffect(() => {
    const handler = () => refetch();
    window.addEventListener(ADMIN_CHANGED_EVENT, handler);
    return () => window.removeEventListener(ADMIN_CHANGED_EVENT, handler);
  }, [refetch]);
}
