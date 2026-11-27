// 角色分配下拉框的候选集。
//
// doc/304 §4 E178 把"没有可证明归属的历史角色"标成了 `quarantined`：它等着平台
// 管理员确认作者，在此之前服务端用 40021 拒绝任何新的指派。列表页把它藏起来是
// 一回事，下拉框照着 `/roles` 的原样渲染是另一回事——平台管理员看到的 `/roles`
// 里**有**这些行，于是他能选中一个必然被拒的选项，点保存才看到错误。
//
// 唯一要保留的是"这个人已经持有的那一行"：把它从候选里删掉，`<select>` 会静静
// 显示第一个选项，界面在说谎（人还持有那个角色），而真相只是"不能新增"。
export interface AssignableRole {
  id: string;
  name: string;
  status?: string;
}

export function assignableRoles(
  roles: AssignableRole[],
  currentRoleID = "",
): AssignableRole[] {
  return roles.filter(
    (role) => role.status !== "quarantined" || role.id === currentRoleID,
  );
}
