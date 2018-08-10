package models

import (
	"context"
	"strings"

	"github.com/yunionio/jsonutils"
	"github.com/yunionio/log"
	"github.com/yunionio/onecloud/pkg/cloudcommon/db"
	"github.com/yunionio/onecloud/pkg/cloudprovider"
	"github.com/yunionio/onecloud/pkg/httperrors"
	"github.com/yunionio/onecloud/pkg/mcclient"
	"github.com/yunionio/pkg/util/compare"
	"github.com/yunionio/pkg/util/secrules"
	"github.com/yunionio/sqlchemy"
)

type SSecurityGroupManager struct {
	db.SSharableVirtualResourceBaseManager
}

var SecurityGroupManager *SSecurityGroupManager

func init() {
	SecurityGroupManager = &SSecurityGroupManager{SSharableVirtualResourceBaseManager: db.NewSharableVirtualResourceBaseManager(SSecurityGroup{}, "secgroups_tbl", "secgroup", "secgroups")}
	SecurityGroupManager.NameRequireAscii = false
}

const (
	SECURITY_GROUP_SEPARATOR = ";"
)

type SSecurityGroup struct {
	db.SSharableVirtualResourceBase
	IsDirty bool `nullable:"false" default:"false"` // Column(Boolean, nullable=False, default=False)
}

func (self *SSecurityGroup) GetGuestsQuery() *sqlchemy.SQuery {
	guests := GuestManager.Query().SubQuery()
	return guests.Query().Filter(sqlchemy.OR(sqlchemy.Equals(guests.Field("secgrp_id"), self.Id),
		sqlchemy.Equals(guests.Field("admin_secgrp_id"), self.Id)))
}

func (self *SSecurityGroup) GetGuestsCount() int {
	return self.GetGuestsQuery().Count()
}

func (self *SSecurityGroup) GetGuests() []SGuest {
	guests := make([]SGuest, 0)
	q := self.GetGuestsQuery()
	err := db.FetchModelObjects(GuestManager, q, &guests)
	if err != nil {
		log.Errorf("GetGuests fail %s", err)
		return nil
	}
	return guests
}

func (self *SSecurityGroup) GetExtraDetails(ctx context.Context, userCred mcclient.TokenCredential, query jsonutils.JSONObject) *jsonutils.JSONDict {
	extra := self.SSharableVirtualResourceBase.GetExtraDetails(ctx, userCred, query)
	extra.Add(jsonutils.NewInt(int64(len(self.GetGuests()))), "guest_cnt")
	extra.Add(jsonutils.NewString(self.getSecurityRuleString()), "rules")
	return extra
}

func (self *SSecurityGroup) GetCustomizeColumns(ctx context.Context, userCred mcclient.TokenCredential, query jsonutils.JSONObject) *jsonutils.JSONDict {
	extra := self.SSharableVirtualResourceBase.GetExtraDetails(ctx, userCred, query)
	extra.Add(jsonutils.NewInt(int64(len(self.GetGuests()))), "guest_cnt")
	extra.Add(jsonutils.NewTimeString(self.CreatedAt), "created_at")
	extra.Add(jsonutils.NewString(self.Description), "description")
	return extra
}

func (manager *SSecurityGroupManager) ValidateCreateData(
	ctx context.Context,
	userCred mcclient.TokenCredential,
	ownerProjId string,
	query jsonutils.JSONObject,
	data *jsonutils.JSONDict,
) (*jsonutils.JSONDict, error) {
	// TODO: check set pending quota
	return manager.SSharableVirtualResourceBaseManager.ValidateCreateData(ctx, userCred, ownerProjId, query, data)
}

func (manager *SSecurityGroupManager) FetchSecgroupById(secId string) *SSecurityGroup {
	if len(secId) > 0 {
		secgrp, _ := manager.FetchById(secId)
		if secgrp != nil {
			return secgrp.(*SSecurityGroup)
		}
	}
	return nil
}

func (self *SSecurityGroup) getSecurityRules() (rules []SSecurityGroupRule) {
	secgrouprules := SecurityGroupRuleManager.Query().SubQuery()
	sql := secgrouprules.Query().Filter(sqlchemy.Equals(secgrouprules.Field("secgroup_id"), self.Id))
	if err := db.FetchModelObjects(SecurityGroupRuleManager, sql, &rules); err != nil {
		log.Errorf("GetGuests fail %s", err)
		return nil
	}
	return
}

func (self *SSecurityGroup) getSecRules() []*secrules.SecurityRule {
	rules := make([]*secrules.SecurityRule, 0)
	for _, rule := range self.getSecurityRules() {
		r, _ := secrules.ParseSecurityRule(rule.GetRule())
		r.Priority = int(rule.Priority)
		rules = append(rules, r)
	}
	return rules
}

func (self *SSecurityGroup) getSecurityRuleString() string {
	secgrouprules := self.getSecurityRules()
	var rules []string
	for _, rule := range secgrouprules {
		rules = append(rules, rule.GetRule())
	}
	return strings.Join(rules, SECURITY_GROUP_SEPARATOR)
}

func totalSecurityGroupCount(projectId string) int {
	q := SecurityGroupManager.Query().Equals("tenant_id", projectId)
	return q.Count()
}

func (self *SSecurityGroup) AllowPerformClone(ctx context.Context, userCred mcclient.TokenCredential, query jsonutils.JSONObject, data jsonutils.JSONObject) bool {
	return true
}

func (self *SSecurityGroup) PerformClone(ctx context.Context, userCred mcclient.TokenCredential, query jsonutils.JSONObject, data jsonutils.JSONObject) (jsonutils.JSONObject, error) {
	if name, _ := data.GetString("name"); len(name) == 0 {
		return nil, httperrors.NewInputParameterError("Missing name params")
	} else {
		sql := SecurityGroupManager.Query()
		sql = SecurityGroupManager.FilterByName(sql, name)
		if sql.Count() != 0 {
			return nil, httperrors.NewDuplicateNameError("Dumplicate name %s", name)
		}
	}

	secgroup := &SSecurityGroup{}
	secgroup.SetModelManager(SecurityGroupManager)

	secgroup.Name, _ = data.GetString("name")
	secgroup.Description, _ = data.GetString("description")
	secgroup.ProjectId = userCred.GetTenantId()
	if err := SecurityGroupManager.TableSpec().Insert(secgroup); err != nil {
		return nil, err
		//db.OpsLog.LogCloneEvent(self, secgroup, userCred, nil)
	}
	secgrouprules := self.getSecurityRules()
	for _, rule := range secgrouprules {
		secgrouprule := &SSecurityGroupRule{}
		secgrouprule.SetModelManager(SecurityGroupRuleManager)

		secgrouprule.Priority = rule.Priority
		secgrouprule.Protocol = rule.Protocol
		secgrouprule.Ports = rule.Ports
		secgrouprule.Direction = rule.Direction
		secgrouprule.CIDR = rule.CIDR
		secgrouprule.Action = rule.Action
		secgrouprule.Description = rule.Description
		secgrouprule.SecgroupID = secgroup.Id
		if err := SecurityGroupRuleManager.TableSpec().Insert(secgrouprule); err != nil {
			return nil, err
		}
	}
	return nil, nil
}

func (manager *SSecurityGroupManager) getSecurityGroups() ([]SSecurityGroup, error) {
	secgroups := make([]SSecurityGroup, 0)
	q := manager.Query()
	if err := db.FetchModelObjects(manager, q, &secgroups); err != nil {
		return nil, err
	} else {
		return secgroups, nil
	}
}

func (manager *SSecurityGroupManager) SyncSecgroups(ctx context.Context, userCred mcclient.TokenCredential, secgroups []cloudprovider.ICloudSecurityGroup) ([]SSecurityGroup, []cloudprovider.ICloudSecurityGroup, compare.SyncResult) {
	localSecgroups := make([]SSecurityGroup, 0)
	remoteSecgroups := make([]cloudprovider.ICloudSecurityGroup, 0)
	syncResult := compare.SyncResult{}

	if dbSecgroups, err := manager.getSecurityGroups(); err != nil {
		syncResult.Error(err)
		return nil, nil, syncResult
	} else {
		removed := make([]SSecurityGroup, 0)
		commondb := make([]SSecurityGroup, 0)
		commonext := make([]cloudprovider.ICloudSecurityGroup, 0)
		added := make([]cloudprovider.ICloudSecurityGroup, 0)
		if err := compare.CompareSets(dbSecgroups, secgroups, &removed, &commondb, &commonext, &added); err != nil {
			syncResult.Error(err)
			return nil, nil, syncResult
		}

		for i := 0; i < len(commondb); i += 1 {
			if err = commondb[i].SyncWithCloudSecurityGroup(userCred, commonext[i]); err != nil {
				syncResult.UpdateError(err)
			} else {
				localSecgroups = append(localSecgroups, commondb[i])
				remoteSecgroups = append(remoteSecgroups, commonext[i])
				syncResult.Update()
			}
		}

		for i := 0; i < len(added); i += 1 {
			if new, err := manager.newFromCloudVpc(added[i]); err != nil {
				syncResult.AddError(err)
			} else {
				localSecgroups = append(localSecgroups, *new)
				remoteSecgroups = append(remoteSecgroups, added[i])
				syncResult.Add()
			}
		}
	}
	return localSecgroups, remoteSecgroups, syncResult
}

func (self *SSecurityGroup) SyncWithCloudSecurityGroup(userCred mcclient.TokenCredential, extSec cloudprovider.ICloudSecurityGroup) error {
	if _, err := self.GetModelManager().TableSpec().Update(self, func() error {
		self.Name = extSec.GetName()
		self.Description = extSec.GetDescription()
		return nil
	}); err != nil {
		log.Errorf("syncWithCloudSecurityGroup error %s", err)
		return err
	}
	return nil
}

func (manager *SSecurityGroupManager) newFromCloudVpc(extSecgroup cloudprovider.ICloudSecurityGroup) (*SSecurityGroup, error) {
	secgroup := SSecurityGroup{}
	secgroup.SetModelManager(manager)
	secgroup.Name = extSecgroup.GetName()
	secgroup.ExternalId = extSecgroup.GetGlobalId()
	secgroup.Description = extSecgroup.GetDescription()

	if err := manager.TableSpec().Insert(&secgroup); err != nil {
		return nil, err
	}
	return &secgroup, nil
}

func (self *SSecurityGroup) DoSync(ctx context.Context, userCred mcclient.TokenCredential) {
	if _, err := self.GetModelManager().TableSpec().Update(self, func() error {
		self.IsDirty = true
		return nil
	}); err != nil {
		log.Errorf("Update Security Group error: %s", err.Error())
	}
	for _, guest := range self.GetGuests() {
		guest.StartSyncTask(ctx, userCred, true, "")
	}
}
