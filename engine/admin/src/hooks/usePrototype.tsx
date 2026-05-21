import { createContext, useContext, useState, type ReactNode } from 'react';

const STORAGE_KEY = 'syntheticbrew_prototype_mode';
const PROTOTYPE_ENABLED = import.meta.env.VITE_PROTOTYPE_ENABLED === 'true'
  || import.meta.env.DEV;

interface PrototypeContextValue {
  isPrototype: boolean;
  togglePrototype: () => void;
  prototypeEnabled: boolean;
}

const PrototypeContext = createContext<PrototypeContextValue>({
  isPrototype: false,
  togglePrototype: () => {},
  prototypeEnabled: false,
});

export function PrototypeProvider({ children }: { children: ReactNode }) {
  const prototypeEnabled = PROTOTYPE_ENABLED;
  const [isPrototype, setIsPrototype] = useState(() => {
    if (!PROTOTYPE_ENABLED) return false;
    return localStorage.getItem(STORAGE_KEY) === 'true';
  });

  // Prototype mode availability is determined entirely by build-time env var
  // (VITE_PROTOTYPE_ENABLED) + localStorage toggle. No server-side API call is
  // made here — doing so would fire an unauthenticated request on every app
  // load, causing a 401 in the console before the user has logged in.

  const togglePrototype = () => {
    setIsPrototype((prev) => {
      const next = !prev;
      localStorage.setItem(STORAGE_KEY, String(next));
      return next;
    });
  };

  return (
    <PrototypeContext.Provider value={{ isPrototype, togglePrototype, prototypeEnabled }}>
      {children}
    </PrototypeContext.Provider>
  );
}

export function usePrototype() {
  return useContext(PrototypeContext);
}
