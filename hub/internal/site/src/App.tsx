import { useStore } from '@nanostores/react'
import { $authed } from '@/stores/auth'
import { $route } from '@/router'
import { Login } from '@/pages/Login'
import { Shell } from '@/components/Shell'
import { Placeholder } from '@/components/Placeholder'
import { Machines } from '@/pages/Machines'
import { MachineDetail } from '@/pages/MachineDetail'
import { Settings } from '@/pages/Settings'

export function App() {
  const authed = useStore($authed)
  const route = useStore($route)
  if (!authed) return <Login />

  return (
    <Shell>
      {route.key === 'machines' && (route.param ? <MachineDetail id={route.param} /> : <Machines />)}
      {route.key === 'settings' && <Settings />}
      {(route.key === 'configsets' || route.key === 'inbox' || route.key === 'credentials') && (
        <Placeholder milestone="M1" />
      )}
      {(route.key === 'overview' || route.key === 'usage' || route.key === 'subscriptions') && (
        <Placeholder milestone="M2" />
      )}
    </Shell>
  )
}
