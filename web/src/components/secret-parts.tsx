import { useQueryClient } from "@tanstack/react-query"
import { Link } from "@tanstack/react-router"
import { toast } from "sonner"
import { CopyIcon, TrashIcon } from "@phosphor-icons/react"
import {
  AlertDialog,
  AlertDialogAction,
  AlertDialogCancel,
  AlertDialogContent,
  AlertDialogDescription,
  AlertDialogFooter,
  AlertDialogHeader,
  AlertDialogTitle,
  AlertDialogTrigger,
} from "@/components/ui/alert-dialog"
import { Badge } from "@/components/ui/badge"
import { Button } from "@/components/ui/button"
import { Spinner } from "@/components/ui/spinner"
import { Ident } from "@/components/ident"
import { $api, errorMessage, type Secret } from "@/lib/api/client"
import { copyText } from "@/lib/clipboard"

/**
 * The secrets Studio manages itself: provider keys (changed in Settings → Providers) and forge
 * credentials. They can't be edited or deleted from a secrets list.
 */
export function useManagedSecrets(env: string) {
  const providers = $api.useQuery("get", "/api/environments/{env}/providers", {
    params: { path: { env } },
  })
  return new Map(
    (providers.data ?? []).flatMap((p) =>
      p.secretId ? [[p.secretId, p.name] as const] : []
    )
  )
}

/** Whether a secret can be edited and deleted from a list. */
export function editable(secret: Secret, managed: Map<string, string>) {
  return !managed.has(secret.id) && !secret.studioOnly
}

/**
 * Where a secret Studio manages is changed instead: the provider that holds the key, or the forge
 * that holds the token.
 */
export function ManagedHint({
  env,
  secret,
  managed,
}: {
  env: string
  secret: Secret
  managed: Map<string, string>
}) {
  const provider = managed.get(secret.id)
  return (
    <Link
      to={provider ? "/e/$env/settings/providers" : "/e/$env/settings/forges"}
      params={{ env }}
      title={
        provider
          ? `Studio manages this key for the provider ${provider}: edit or remove it there`
          : "Studio manages this credential for a git forge: replace or remove it there"
      }
      className="text-xs whitespace-nowrap text-muted-foreground underline-offset-2 hover:text-foreground hover:underline"
    >
      {provider ? "In Providers" : "In Git forges"}
    </Link>
  )
}

/** A secret's name in mono, with a badge when Studio manages it. */
export function SecretName({
  secret,
  managed,
}: {
  secret: Secret
  managed: Map<string, string>
}) {
  return (
    <>
      <Ident>{secret.name}</Ident>
      {managed.has(secret.id) && (
        <Badge variant="secondary" className="ml-1.5 font-sans">
          Provider {managed.get(secret.id)}
        </Badge>
      )}
      {secret.studioOnly && (
        <Badge variant="secondary" className="ml-1.5 font-sans">
          {secret.name.endsWith("_SSH_KEY") ? "Forge SSH key" : "Forge token"}
        </Badge>
      )}
    </>
  )
}

export function SecretHosts({ secret }: { secret: Secret }) {
  return (
    <div className="flex max-w-xs flex-wrap gap-x-2 gap-y-0.5">
      {(secret.hosts ?? []).map((host) => (
        <Ident key={host}>{host}</Ident>
      ))}
    </div>
  )
}

/** The placeholder sandboxes see, cut to fit, with a copy button. */
export function SecretPlaceholder({ secret }: { secret: Secret }) {
  return (
    <div className="flex min-w-0 items-center gap-1">
      <span className="block max-w-40 min-w-0 truncate text-muted-foreground">
        <Ident>{secret.placeholder}</Ident>
      </span>
      <Button
        variant="ghost"
        size="icon-xs"
        title="Copy placeholder"
        aria-label="Copy placeholder"
        onClick={() => copy(secret.placeholder)}
      >
        <CopyIcon />
      </Button>
    </div>
  )
}

async function copy(text: string) {
  try {
    await copyText(text)
    toast.success("Copied")
  } catch {
    toast.error("Could not copy")
  }
}

export function DeleteSecret({ env, secret }: { env: string; secret: Secret }) {
  const queryClient = useQueryClient()
  const remove = $api.useMutation(
    "delete",
    "/api/environments/{env}/secrets/{id}",
    {
      onSettled: () => {
        queryClient.invalidateQueries({
          queryKey: ["get", "/api/environments/{env}/secrets"],
        })
      },
      onError: (err) =>
        toast.error("Could not delete the secret", {
          description: errorMessage(err),
        }),
    }
  )

  return (
    <AlertDialog>
      <AlertDialogTrigger
        render={
          <Button
            variant="ghost"
            size="icon-sm"
            title="Delete secret"
            aria-label={`Delete ${secret.name}`}
          />
        }
      >
        <TrashIcon />
      </AlertDialogTrigger>
      <AlertDialogContent>
        <AlertDialogHeader>
          <AlertDialogTitle>Delete {secret.name}?</AlertDialogTitle>
          <AlertDialogDescription>
            {secret.browserOnly
              ? "The browser no longer fills it in or sends it to the sites that ask for it."
              : "Requests that use its placeholder will fail until you add it again."}
          </AlertDialogDescription>
        </AlertDialogHeader>
        <AlertDialogFooter>
          <AlertDialogCancel>Cancel</AlertDialogCancel>
          <AlertDialogAction
            variant="destructive"
            disabled={remove.isPending}
            onClick={() =>
              remove.mutate({ params: { path: { env, id: secret.id } } })
            }
          >
            {remove.isPending && <Spinner />}
            Delete
          </AlertDialogAction>
        </AlertDialogFooter>
      </AlertDialogContent>
    </AlertDialog>
  )
}
