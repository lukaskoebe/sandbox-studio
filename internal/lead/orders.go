package lead

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"github.com/lukaskoebe/sandbox-studio/internal/events"
	"github.com/lukaskoebe/sandbox-studio/internal/store"
)

// Standing orders (docs/lead.md): the user's words for a plan Lead drafts on its own at a set
// time, such as "each morning, hand failing checks to whoever is idle". A plan drafted for an
// order waits for approval as any plan does; the order only saves asking for it.

// MaxOrders is how many standing orders an environment may have.
const MaxOrders = 20

var (
	// ErrTooManyOrders is an environment that has MaxOrders already.
	ErrTooManyOrders = errors.New("an environment has at most 20 standing orders; delete one first")
	// ErrNoDays is an order that would never come due.
	ErrNoDays = errors.New("pick at least one day")
)

// OrderInput is a standing order as the user sets it.
type OrderInput struct {
	Text    string `json:"text" minLength:"1" maxLength:"2000" doc:"What Lead should do each time, as a plan's first message"`
	At      int    `json:"at" minimum:"0" maximum:"1439" doc:"When it comes due, in minutes after local midnight"`
	Days    int    `json:"days" minimum:"1" maximum:"127" doc:"The days it comes due on: bit 0 is Monday, bit 6 Sunday"`
	Enabled bool   `json:"enabled"`
}

// PlanHead is a plan as a list names it.
type PlanHead struct {
	ID    string `json:"id"`
	Title string `json:"title"`
	State string `json:"state" enum:"drafting,asking,proposed,running,done,cancelled,failed"`
}

// OrderView is a standing order as its page shows it.
type OrderView struct {
	ID        string     `json:"id"`
	Text      string     `json:"text"`
	At        int        `json:"at" doc:"Minutes after local midnight"`
	Days      int        `json:"days" doc:"Bit 0 is Monday, bit 6 Sunday"`
	Enabled   bool       `json:"enabled"`
	LastDay   string     `json:"lastDay,omitempty" doc:"The local date it last came due, YYYY-MM-DD"`
	LastPlan  *PlanHead  `json:"lastPlan,omitempty" doc:"The plan it drafted last, while it is there"`
	Note      string     `json:"note,omitempty" doc:"Why it drafted no plan the last time it came due"`
	Next      *time.Time `json:"next,omitempty" doc:"When it comes due next; none while it is off"`
	CreatedAt time.Time  `json:"createdAt"`
}

func (s *Service) publishOrders(env string) {
	if s.Bus != nil {
		s.Bus.Publish(events.Event{Topic: events.TopicLead, EnvironmentID: env, ID: "orders"})
	}
}

// Orders are the environment's standing orders, oldest first.
func (s *Service) Orders(ctx context.Context, env string) ([]OrderView, error) {
	orders, err := s.Store.LeadOrders(ctx, env)
	if err != nil {
		return nil, err
	}
	out := make([]OrderView, 0, len(orders))
	for _, o := range orders {
		out = append(out, s.orderView(ctx, o))
	}
	return out, nil
}

func (s *Service) orderView(ctx context.Context, o store.LeadOrder) OrderView {
	v := OrderView{ID: o.ID, Text: o.Text, At: o.At, Days: o.Days, Enabled: o.Enabled, LastDay: o.LastDay, Note: o.Note, CreatedAt: o.CreatedAt}
	if o.LastPlanID != "" {
		if p, err := s.Store.GetLeadPlan(ctx, o.EnvironmentID, o.LastPlanID); err == nil {
			v.LastPlan = &PlanHead{ID: p.ID, Title: p.Title, State: p.State}
		}
	}
	if o.Enabled {
		if next, ok := nextDue(o, s.now()); ok {
			v.Next = &next
		}
	}
	return v
}

// nextDue is when an order comes due next: today at its time if that is still to come and it
// hasn't come due today, else the next day it is on.
func nextDue(o store.LeadOrder, now time.Time) (time.Time, bool) {
	for d := 0; d <= 7; d++ {
		day := now.AddDate(0, 0, d)
		at := time.Date(day.Year(), day.Month(), day.Day(), o.At/60, o.At%60, 0, 0, now.Location())
		if o.Days&weekdayBit(day) == 0 || d == 0 && o.LastDay == day.Format(time.DateOnly) {
			continue
		}
		if d > 0 || !at.Before(now) {
			return at, true
		}
	}
	return time.Time{}, false
}

func checkOrder(in OrderInput) (OrderInput, error) {
	in.Text = strings.TrimSpace(in.Text)
	switch {
	case in.Text == "":
		return in, ErrNoMessage
	case in.Days&0b1111111 == 0:
		return in, ErrNoDays
	}
	in.Text = clip(in.Text, MaxPlanText)
	in.At = min(max(in.At, 0), 24*60-1)
	in.Days &= 0b1111111
	return in, nil
}

// settle marks an order as having come due today when its time today has passed, so a new or
// changed order first comes due at its next time rather than at once.
func settle(o *store.LeadOrder, now time.Time) {
	if now.Hour()*60+now.Minute() >= o.At {
		o.LastDay = now.Format(time.DateOnly)
	}
}

// AddOrder saves a new standing order.
func (s *Service) AddOrder(ctx context.Context, env string, in OrderInput) (OrderView, error) {
	in, err := checkOrder(in)
	if err != nil {
		return OrderView{}, err
	}
	s.orderMu.Lock() // two at once can't both pass the limit
	defer s.orderMu.Unlock()
	orders, err := s.Store.LeadOrders(ctx, env)
	if err != nil {
		return OrderView{}, err
	}
	if len(orders) >= MaxOrders {
		return OrderView{}, ErrTooManyOrders
	}
	now := s.now()
	o := store.LeadOrder{EnvironmentID: env, Text: in.Text, At: in.At, Days: in.Days, Enabled: in.Enabled, CreatedAt: now}
	settle(&o, now)
	o, err = s.Store.AddLeadOrder(ctx, o)
	if err != nil {
		return OrderView{}, err
	}
	s.publishOrders(env)
	return s.orderView(ctx, o), nil
}

// SetOrder changes a standing order.
func (s *Service) SetOrder(ctx context.Context, env, id string, in OrderInput) (OrderView, error) {
	in, err := checkOrder(in)
	if err != nil {
		return OrderView{}, err
	}
	s.orderMu.Lock()
	defer s.orderMu.Unlock()
	o, err := s.Store.GetLeadOrder(ctx, env, id)
	if err != nil {
		return OrderView{}, err
	}
	now := s.now()
	retimed := o.At != in.At || o.Days != in.Days || !o.Enabled && in.Enabled
	o.Text, o.At, o.Days, o.Enabled, o.UpdatedAt = in.Text, in.At, in.Days, in.Enabled, now
	if retimed {
		// Moved to later today, it still comes due today, unless it already drafted today.
		today := now.Format(time.DateOnly)
		if o.LastDay == today && !s.draftedOn(ctx, env, o, today) {
			o.LastDay = ""
		}
		settle(&o, now)
	}
	if err := s.Store.SaveLeadOrder(ctx, o); err != nil {
		return OrderView{}, err
	}
	s.publishOrders(env)
	return s.orderView(ctx, o), nil
}

// draftedOn is whether the order's last plan was drafted that day.
func (s *Service) draftedOn(ctx context.Context, env string, o store.LeadOrder, day string) bool {
	if o.LastPlanID == "" {
		return false
	}
	p, err := s.Store.GetLeadPlan(ctx, env, o.LastPlanID)
	return err == nil && p.CreatedAt.In(s.now().Location()).Format(time.DateOnly) == day
}

// DeleteOrder removes a standing order. The plans it drafted stay.
func (s *Service) DeleteOrder(ctx context.Context, env, id string) error {
	if err := s.Store.DeleteLeadOrder(ctx, env, id); err != nil {
		return err
	}
	s.publishOrders(env)
	return nil
}

// RunOrder drafts an order's plan now, whether or not it is on, and leaves its schedule as it is.
func (s *Service) RunOrder(ctx context.Context, env, id string) (LeadPlanView, error) {
	s.orderMu.Lock()
	defer s.orderMu.Unlock()
	o, err := s.Store.GetLeadOrder(ctx, env, id)
	if err != nil {
		return LeadPlanView{}, err
	}
	return s.draftOrder(ctx, o)
}

// draftOrder starts the order's plan and records it as the order's last.
func (s *Service) draftOrder(ctx context.Context, o store.LeadOrder) (LeadPlanView, error) {
	p, err := s.newPlan(ctx, o.EnvironmentID, o.Text, o.ID)
	if err != nil {
		return LeadPlanView{}, err
	}
	o.LastPlanID, o.Note, o.UpdatedAt = p.ID, "", s.now()
	if err := s.Store.SaveLeadOrder(context.WithoutCancel(ctx), o); err != nil {
		s.log().Warn("lead: saving a standing order failed", "env", o.EnvironmentID, "err", err)
	}
	s.publishOrders(o.EnvironmentID)
	return p, nil
}

// orders drafts the plans of the environment's orders that are due: on, on today, past their
// time, and not yet due today. Studio that wasn't running at an order's time drafts it when it
// starts, the same day only. An order whose last plan is still open drafts none and says so.
func (s *Service) orders(ctx context.Context, env string, now time.Time) {
	if s.Send == nil {
		return
	}
	s.orderMu.Lock()
	defer s.orderMu.Unlock()
	orders, err := s.Store.LeadOrders(ctx, env)
	if err != nil {
		s.log().Warn("lead: reading standing orders failed", "env", env, "err", err)
		return
	}
	today := now.Format(time.DateOnly)
	for _, o := range orders {
		if !o.Enabled || o.Days&weekdayBit(now) == 0 || now.Hour()*60+now.Minute() < o.At || o.LastDay == today {
			continue
		}
		o.LastDay, o.UpdatedAt = today, now
		if o.LastPlanID != "" {
			if p, err := s.Store.GetLeadPlan(ctx, env, o.LastPlanID); err == nil && s.openPlan(ctx, p) {
				o.Note = "Skipped " + now.Format("Mon 15:04") + ": the plan from last time is still open."
				if err := s.Store.SaveLeadOrder(ctx, o); err != nil {
					s.log().Warn("lead: saving a standing order failed", "env", env, "err", err)
				}
				s.publishOrders(env)
				continue
			}
		}
		if err := s.Store.SaveLeadOrder(ctx, o); err != nil { // due once, even if drafting fails
			s.log().Warn("lead: saving a standing order failed", "env", env, "err", err)
			continue
		}
		if _, err := s.draftOrder(ctx, o); err != nil {
			s.log().Warn("lead: drafting a standing order's plan failed", "env", env, "err", err)
		}
	}
}

// openPlan is whether a plan still waits on Lead, the user or the teammates. One that asks
// waits only while Lead's question is the last word: a draft that failed, or a reply without
// steps, leaves it asking with nothing to answer, and the order goes on the next time.
func (s *Service) openPlan(ctx context.Context, p store.LeadPlan) bool {
	switch p.State {
	case store.LeadPlanDone, store.LeadPlanCancelled, store.LeadPlanFailed:
		return false
	case store.LeadPlanAsking:
		items, err := s.Store.LeadPlanItems(ctx, p.ID)
		if err != nil {
			return true
		}
		for i := len(items) - 1; i >= 0; i-- {
			if items[i].Kind == ItemNote {
				continue
			}
			var d itemData
			_ = json.Unmarshal(items[i].Data, &d)
			return items[i].Kind == ItemLead && d.Question != nil
		}
		return false
	}
	return true
}
