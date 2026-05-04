"use client";

import { useEffect, useLayoutEffect, useMemo, useRef, useState } from "react";
import { createPortal } from "react-dom";
import { Crown, Shield, User, Plus, MoreHorizontal, UserMinus, Users, Clock, X, Mail } from "lucide-react";
import { ActorAvatar } from "../../common/actor-avatar";
import type { MemberWithUser, MemberRole, Invitation, VelafiDirectoryEntry } from "@multica/core/types";
import { Input } from "@multica/ui/components/ui/input";
import { Button } from "@multica/ui/components/ui/button";
import { Card, CardContent } from "@multica/ui/components/ui/card";
import { Badge } from "@multica/ui/components/ui/badge";
import { Avatar, AvatarFallback } from "@multica/ui/components/ui/avatar";
import {
  AlertDialog,
  AlertDialogContent,
  AlertDialogHeader,
  AlertDialogTitle,
  AlertDialogDescription,
  AlertDialogFooter,
  AlertDialogCancel,
  AlertDialogAction,
} from "@multica/ui/components/ui/alert-dialog";
import {
  Select,
  SelectTrigger,
  SelectValue,
  SelectContent,
  SelectItem,
} from "@multica/ui/components/ui/select";
import {
  DropdownMenu,
  DropdownMenuTrigger,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuSeparator,
  DropdownMenuSub,
  DropdownMenuSubTrigger,
  DropdownMenuSubContent,
} from "@multica/ui/components/ui/dropdown-menu";
import { toast } from "sonner";
import { useQuery, useQueryClient } from "@tanstack/react-query";
import { useAuthStore } from "@multica/core/auth";
import { useWorkspaceId } from "@multica/core/hooks";
import { useCurrentWorkspace } from "@multica/core/paths";
import { memberListOptions, invitationListOptions, workspaceKeys } from "@multica/core/workspace/queries";
import { api } from "@multica/core/api";

const roleConfig: Record<MemberRole, { label: string; icon: typeof Crown; description: string }> = {
  owner: { label: "Owner", icon: Crown, description: "Full access, manage all settings" },
  admin: { label: "Admin", icon: Shield, description: "Manage members and settings" },
  member: { label: "Member", icon: User, description: "Create and work on issues" },
};

function MemberRow({
  member,
  canManage,
  canManageOwners,
  ownerCount,
  isSelf,
  busy,
  onRoleChange,
  onRemove,
}: {
  member: MemberWithUser;
  canManage: boolean;
  canManageOwners: boolean;
  /** Total number of owners in this workspace — needed to gate demoting the
   *  last owner per `workspace.go:497-507`. */
  ownerCount: number;
  isSelf: boolean;
  busy: boolean;
  onRoleChange: (role: MemberRole) => void;
  onRemove: () => void;
}) {
  const rc = roleConfig[member.role];
  const RoleIcon = rc.icon;
  const canEditRole = canManage && !isSelf && (member.role !== "owner" || canManageOwners);
  const canRemove = canManage && !isSelf && (member.role !== "owner" || canManageOwners);
  const isLastOwner = member.role === "owner" && ownerCount <= 1;
  const showMenu = canEditRole || canRemove;

  return (
    <div className="flex items-center gap-3 px-4 py-3">
      <ActorAvatar actorType="member" actorId={member.user_id} size={32} />
      <div className="min-w-0 flex-1">
        <div className="text-sm font-medium truncate">{member.name}</div>
        <div className="text-xs text-muted-foreground truncate">{member.email}</div>
      </div>
      {showMenu && (
        <DropdownMenu>
          <DropdownMenuTrigger
            render={
              <Button variant="ghost" size="icon-sm" disabled={busy}>
                <MoreHorizontal className="h-4 w-4 text-muted-foreground" />
              </Button>
            }
          />
          <DropdownMenuContent align="end" className="w-auto">
            {canEditRole && (
              <DropdownMenuSub>
                <DropdownMenuSubTrigger>
                  <Shield className="h-3.5 w-3.5" />
                  Change role
                </DropdownMenuSubTrigger>
                <DropdownMenuSubContent className="w-auto">
                  {(Object.entries(roleConfig) as [MemberRole, (typeof roleConfig)[MemberRole]][]).map(
                    ([role, config]) => {
                      if (role === "owner" && !canManageOwners) return null;
                      const Icon = config.icon;
                      // Demoting the last owner would leave the workspace
                      // ownerless — server rejects with 400, mirror that
                      // here as a disabled option with explanation.
                      const wouldDemoteLastOwner =
                        isLastOwner && role !== "owner";
                      return (
                        <DropdownMenuItem
                          key={role}
                          onClick={() =>
                            wouldDemoteLastOwner ? undefined : onRoleChange(role)
                          }
                          disabled={wouldDemoteLastOwner}
                          title={
                            wouldDemoteLastOwner
                              ? "Promote another member to owner first — a workspace must keep at least one owner."
                              : undefined
                          }
                        >
                          <Icon className="h-3.5 w-3.5" />
                          <div className="flex flex-col">
                            <span>{config.label}</span>
                            <span className="text-xs text-muted-foreground font-normal">
                              {wouldDemoteLastOwner
                                ? "Cannot demote the last owner"
                                : config.description}
                            </span>
                          </div>
                          {member.role === role && (
                            <span className="ml-auto text-xs text-muted-foreground">&#10003;</span>
                          )}
                        </DropdownMenuItem>
                      );
                    }
                  )}
                </DropdownMenuSubContent>
              </DropdownMenuSub>
            )}
            {canEditRole && canRemove && <DropdownMenuSeparator />}
            {canRemove && (
              <DropdownMenuItem variant="destructive" onClick={onRemove}>
                <UserMinus className="h-3.5 w-3.5" />
                Remove from workspace
              </DropdownMenuItem>
            )}
          </DropdownMenuContent>
        </DropdownMenu>
      )}
      <Badge variant="secondary">
        <RoleIcon className="h-3 w-3" />
        {rc.label}
      </Badge>
    </div>
  );
}

function InvitationRow({
  invitation,
  canManage,
  onRevoke,
  busy,
}: {
  invitation: Invitation;
  canManage: boolean;
  onRevoke: () => void;
  busy: boolean;
}) {
  const rc = roleConfig[invitation.role];

  return (
    <div className="flex items-center gap-3 px-4 py-3">
      <div className="flex h-8 w-8 items-center justify-center rounded-full bg-muted">
        <Mail className="h-4 w-4 text-muted-foreground" />
      </div>
      <div className="min-w-0 flex-1">
        <div className="text-sm font-medium truncate">{invitation.invitee_email}</div>
        <div className="flex items-center gap-1 text-xs text-muted-foreground">
          <Clock className="h-3 w-3" />
          <span>Pending</span>
        </div>
      </div>
      {canManage && (
        <Button
          variant="ghost"
          size="icon-sm"
          disabled={busy}
          onClick={onRevoke}
          title="Revoke invitation"
        >
          <X className="h-4 w-4 text-muted-foreground" />
        </Button>
      )}
      <Badge variant="outline">
        {rc.label}
      </Badge>
    </div>
  );
}

export function MembersTab() {
  const user = useAuthStore((s) => s.user);
  const workspace = useCurrentWorkspace();
  const qc = useQueryClient();
  const wsId = useWorkspaceId();
  const { data: members = [] } = useQuery(memberListOptions(wsId));
  const { data: invitations = [] } = useQuery(invitationListOptions(wsId));

  const [inviteRole, setInviteRole] = useState<MemberRole>("member");
  const [quickAddLoading, setQuickAddLoading] = useState<string | null>(null); // email being added, null when idle

  // Velafi-quick-add autocomplete state.
  const [searchQuery, setSearchQuery] = useState("");
  const [showDropdown, setShowDropdown] = useState(false);
  const inputRef = useRef<HTMLInputElement>(null);
  const dropdownRef = useRef<HTMLDivElement>(null);
  const [dropdownRect, setDropdownRect] = useState<{ top: number; left: number; width: number } | null>(null);

  // Fetch all Velafi roster (capped at 100) once per workspace; client-side
  // filter on each keystroke. Tenant size <100 so this is cheap.
  const { data: directoryResp } = useQuery({
    queryKey: ["velafi-directory", wsId],
    queryFn: () => api.velafiDirectorySearch(wsId, ""),
    staleTime: 5 * 60_000,
    enabled: !!wsId,
  });
  const allEntries = directoryResp?.results ?? [];

  // Client-side filter on the cached roster. Only render results when the
  // user has typed at least 1 character — prevents rendering 71 rows on focus.
  const filteredEntries = useMemo(() => {
    const q = searchQuery.trim().toLowerCase();
    if (!q) return [];
    return allEntries
      .filter((e) => {
        const hay = `${e.name} ${e.email} ${e.job_title ?? ""}`.toLowerCase();
        return hay.includes(q);
      })
      .slice(0, 12);
  }, [allEntries, searchQuery]);

  // Position the portal dropdown beneath the input. Recompute on scroll/resize
  // while the dropdown is open.
  useLayoutEffect(() => {
    if (!showDropdown || !inputRef.current) {
      setDropdownRect(null);
      return;
    }
    const compute = () => {
      const r = inputRef.current?.getBoundingClientRect();
      if (r) setDropdownRect({ top: r.bottom + 4, left: r.left, width: r.width });
    };
    compute();
    window.addEventListener("scroll", compute, true);
    window.addEventListener("resize", compute);
    return () => {
      window.removeEventListener("scroll", compute, true);
      window.removeEventListener("resize", compute);
    };
  }, [showDropdown]);

  // Close dropdown on outside click.
  useEffect(() => {
    if (!showDropdown) return;
    const handler = (e: MouseEvent) => {
      const t = e.target as Node;
      if (
        inputRef.current && !inputRef.current.contains(t) &&
        dropdownRef.current && !dropdownRef.current.contains(t)
      ) {
        setShowDropdown(false);
      }
    };
    document.addEventListener("mousedown", handler);
    return () => document.removeEventListener("mousedown", handler);
  }, [showDropdown]);
  const [memberActionId, setMemberActionId] = useState<string | null>(null);
  const [invitationActionId, setInvitationActionId] = useState<string | null>(null);
  const [confirmAction, setConfirmAction] = useState<{
    title: string;
    description: string;
    variant?: "destructive";
    onConfirm: () => Promise<void>;
  } | null>(null);

  const currentMember = members.find((m) => m.user_id === user?.id) ?? null;
  const canManageWorkspace = currentMember?.role === "owner" || currentMember?.role === "admin";
  const isOwner = currentMember?.role === "owner";
  const ownerCount = members.filter((m) => m.role === "owner").length;

  // Velafi fork: direct add member without invitation/email confirmation.
  // Restricted to Velafi tenant domains (velafi.com / galactic.holdings).
  // Click on a roster entry → immediately add with the currently selected role.
  const handleVelafiQuickAddByEntry = async (entry: VelafiDirectoryEntry) => {
    if (!workspace || entry.already_member || quickAddLoading) return;
    setQuickAddLoading(entry.email);
    try {
      const result = await api.velafiQuickAdd(workspace.id, {
        email: entry.email,
        role: inviteRole,
      });
      qc.invalidateQueries({ queryKey: workspaceKeys.members(wsId) });
      qc.invalidateQueries({ queryKey: ["velafi-directory", wsId] });
      const pending = result.is_pending_login ? " (pending first login)" : "";
      toast.success(`Added ${result.user.name || result.user.email}${pending}`);
      setSearchQuery("");
      setShowDropdown(false);
    } catch (e) {
      toast.error(e instanceof Error ? e.message : "Failed to add member");
    } finally {
      setQuickAddLoading(null);
    }
  };

  const handleRevokeInvitation = (invitation: Invitation) => {
    if (!workspace) return;
    setConfirmAction({
      title: "Revoke invitation",
      description: `Revoke the invitation to ${invitation.invitee_email}? They will no longer be able to join this workspace.`,
      variant: "destructive",
      onConfirm: async () => {
        setInvitationActionId(invitation.id);
        try {
          await api.revokeInvitation(workspace.id, invitation.id);
          qc.invalidateQueries({ queryKey: workspaceKeys.invitations(wsId) });
          toast.success("Invitation revoked");
        } catch (e) {
          toast.error(e instanceof Error ? e.message : "Failed to revoke invitation");
        } finally {
          setInvitationActionId(null);
        }
      },
    });
  };

  const handleRoleChange = async (memberId: string, role: MemberRole) => {
    if (!workspace) return;
    setMemberActionId(memberId);
    try {
      await api.updateMember(workspace.id, memberId, { role });
      qc.invalidateQueries({ queryKey: workspaceKeys.members(wsId) });
      toast.success("Role updated");
    } catch (e) {
      toast.error(e instanceof Error ? e.message : "Failed to update member");
    } finally {
      setMemberActionId(null);
    }
  };

  const handleRemoveMember = (member: MemberWithUser) => {
    if (!workspace) return;
    setConfirmAction({
      title: `Remove ${member.name}`,
      description: `Remove ${member.name} from ${workspace.name}? They will lose access to this workspace.`,
      variant: "destructive",
      onConfirm: async () => {
        setMemberActionId(member.id);
        try {
          await api.deleteMember(workspace.id, member.id);
          qc.invalidateQueries({ queryKey: workspaceKeys.members(wsId) });
          toast.success("Member removed");
        } catch (e) {
          toast.error(e instanceof Error ? e.message : "Failed to remove member");
        } finally {
          setMemberActionId(null);
        }
      },
    });
  };

  if (!workspace) return null;

  return (
    <div className="space-y-8">
      <section className="space-y-4">
        <div className="flex items-center gap-2">
          <Users className="h-4 w-4 text-muted-foreground" />
          <h2 className="text-sm font-semibold">Members ({members.length})</h2>
        </div>

        {canManageWorkspace && (
          <Card>
            <CardContent className="space-y-3">
              <div className="flex items-center gap-2">
                <Plus className="h-4 w-4 text-muted-foreground" />
                <h3 className="text-sm font-medium">Add member</h3>
              </div>
              {/* Velafi fork: type a name → autocomplete dropdown shows matching
                  Lark roster entries (avatar + name + email) → click to add.
                  Dropdown rendered via React Portal to escape Card overflow. */}
              <div className="grid gap-3 sm:grid-cols-[1fr_120px]">
                <Input
                  ref={inputRef}
                  type="text"
                  value={searchQuery}
                  onChange={(e) => {
                    setSearchQuery(e.target.value);
                    setShowDropdown(true);
                  }}
                  onFocus={() => setShowDropdown(true)}
                  placeholder="Type to search Velafi members…"
                />
                <Select value={inviteRole} onValueChange={(value) => setInviteRole(value as MemberRole)}>
                  <SelectTrigger size="sm">
                    <SelectValue>{() => roleConfig[inviteRole].label}</SelectValue>
                  </SelectTrigger>
                  <SelectContent>
                    <SelectItem value="member">{roleConfig.member.label}</SelectItem>
                    <SelectItem value="admin">{roleConfig.admin.label}</SelectItem>
                  </SelectContent>
                </Select>
              </div>
              {showDropdown && dropdownRect && searchQuery && typeof document !== "undefined" &&
                createPortal(
                  <div
                    ref={dropdownRef}
                    className="absolute z-[1000] max-h-72 overflow-y-auto rounded-md border bg-popover shadow-lg"
                    style={{
                      top: dropdownRect.top,
                      left: dropdownRect.left,
                      width: dropdownRect.width,
                    }}
                  >
                    {filteredEntries.length === 0 ? (
                      <div className="px-3 py-2 text-xs text-muted-foreground">
                        No Velafi members match "{searchQuery}".
                      </div>
                    ) : (
                      filteredEntries.map((entry) => {
                        const isAdding = quickAddLoading === entry.email;
                        const initials = entry.name
                          ? entry.name.charAt(0).toUpperCase()
                          : entry.email.charAt(0).toUpperCase();
                        return (
                          <button
                            key={entry.email}
                            type="button"
                            disabled={entry.already_member || !!quickAddLoading}
                            onClick={() => handleVelafiQuickAddByEntry(entry)}
                            className="flex w-full items-center gap-3 px-3 py-2 text-left transition-colors hover:bg-accent disabled:cursor-not-allowed disabled:opacity-50"
                          >
                            <Avatar size="sm">
                              <AvatarFallback>{initials}</AvatarFallback>
                            </Avatar>
                            <div className="min-w-0 flex-1">
                              <div className="truncate text-sm font-medium">
                                {entry.name || entry.email}
                              </div>
                              <div className="truncate text-xs text-muted-foreground">
                                {entry.email}
                                {entry.job_title ? ` · ${entry.job_title}` : ""}
                              </div>
                            </div>
                            {entry.already_member && (
                              <Badge variant="secondary" className="shrink-0 text-[10px]">Member</Badge>
                            )}
                            {isAdding && (
                              <span className="shrink-0 text-xs text-muted-foreground">Adding…</span>
                            )}
                          </button>
                        );
                      })
                    )}
                  </div>,
                  document.body
                )
              }
            </CardContent>
          </Card>
        )}

        {members.length > 0 ? (
          <div className="overflow-hidden rounded-xl ring-1 ring-foreground/10">
            {members.map((m, i) => (
              <div key={m.id} className={i > 0 ? "border-t border-border/50" : ""}>
                <MemberRow
                  member={m}
                  canManage={canManageWorkspace}
                  canManageOwners={isOwner}
                  ownerCount={ownerCount}
                  isSelf={m.user_id === user?.id}
                  busy={memberActionId === m.id}
                  onRoleChange={(role) => handleRoleChange(m.id, role)}
                  onRemove={() => handleRemoveMember(m)}
                />
              </div>
            ))}
          </div>
        ) : (
          <p className="text-sm text-muted-foreground">No members found.</p>
        )}
      </section>

      {invitations.length > 0 && (
        <section className="space-y-4">
          <div className="flex items-center gap-2">
            <Clock className="h-4 w-4 text-muted-foreground" />
            <h2 className="text-sm font-semibold">Pending invitations ({invitations.length})</h2>
          </div>
          <div className="overflow-hidden rounded-xl ring-1 ring-foreground/10">
            {invitations.map((inv, i) => (
              <div key={inv.id} className={i > 0 ? "border-t border-border/50" : ""}>
                <InvitationRow
                  invitation={inv}
                  canManage={canManageWorkspace}
                  onRevoke={() => handleRevokeInvitation(inv)}
                  busy={invitationActionId === inv.id}
                />
              </div>
            ))}
          </div>
        </section>
      )}

      <AlertDialog open={!!confirmAction} onOpenChange={(v) => { if (!v) setConfirmAction(null); }}>
        <AlertDialogContent>
          <AlertDialogHeader>
            <AlertDialogTitle>{confirmAction?.title}</AlertDialogTitle>
            <AlertDialogDescription>{confirmAction?.description}</AlertDialogDescription>
          </AlertDialogHeader>
          <AlertDialogFooter>
            <AlertDialogCancel>Cancel</AlertDialogCancel>
            <AlertDialogAction
              variant={confirmAction?.variant === "destructive" ? "destructive" : "default"}
              onClick={async () => {
                await confirmAction?.onConfirm();
                setConfirmAction(null);
              }}
            >
              Confirm
            </AlertDialogAction>
          </AlertDialogFooter>
        </AlertDialogContent>
      </AlertDialog>
    </div>
  );
}
