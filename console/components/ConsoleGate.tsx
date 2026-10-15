'use client';

import { useCallback, useEffect, useState } from 'react';
import { api, ApiError } from '@/lib/api';
import type { State } from '@/lib/types';
import { Console } from './Console';
import { SignInGate } from './SignInGate';

/** Chooses between the sign-in gate and the console.
 *
 * The split is the point: the document itself is served signed-out so there is something
 * to click, but no data is fetched until a session exists, and every API route rejects an
 * unauthenticated request independently. */
export function ConsoleGate() {
  const [state, setState] = useState<State | null>(null);
  const [checked, setChecked] = useState(false);

  const load = useCallback(async () => {
    try {
      setState(await api.state());
    } catch {
      setState(null);
    } finally {
      setChecked(true);
    }
  }, []);

  useEffect(() => {
    void load();
  }, [load]);

  if (!checked) {
    return (
      <div className="signin">
        <div className="card">
          <span className="spinner" /> Loading…
        </div>
      </div>
    );
  }

  if (!state) return <SignInGate />;
  return <Console initial={state} reload={load} />;
}
