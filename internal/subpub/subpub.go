package subpub

import (
	"context"
	"fmt"
	"sync"
)

const defaultSubscriberMessageBufferSize = 128

type MessageHandler func(msg interface{})

type Subscription interface {
	Unsubscribe()
}

type SubPub interface {
	Subscribe(subject string, cb MessageHandler) (Subscription, error)
	Publish(subject string, msg interface{}) error
	Close(ctx context.Context) error
}

type Subscriber struct {
	subject        string
	messages       chan interface{}
	handler        MessageHandler
	spLink         *SubPubImplementation
	ctx            context.Context
	cancel         context.CancelFunc
	unsubOnce      sync.Once
	msgMu          sync.Mutex
	messagesClosed bool
}

type SubPubImplementation struct {
	subjects  map[string]map[*Subscriber]struct{}
	mu        sync.RWMutex
	ctx       context.Context
	cancel    context.CancelFunc
	closed    bool
	closeOnce sync.Once
	wg        sync.WaitGroup
}

func NewSubPub() SubPub {
	rootCtx, rootCancel := context.WithCancel(context.Background())
	return &SubPubImplementation{
		subjects: make(map[string]map[*Subscriber]struct{}),
		ctx:      rootCtx,
		cancel:   rootCancel,
	}
}

func (sp *SubPubImplementation) Subscribe(subject string, cb MessageHandler) (Subscription, error) {
	sp.mu.Lock()
	if sp.closed {
		sp.mu.Unlock()
		return nil, fmt.Errorf("subpub: system is closed")
	}
	subCtx, subCancel := context.WithCancel(sp.ctx)
	newSubscriber := &Subscriber{
		subject:  subject,
		handler:  cb,
		messages: make(chan interface{}, defaultSubscriberMessageBufferSize),
		spLink:   sp,
		ctx:      subCtx,
		cancel:   subCancel,
	}
	if _, ok := sp.subjects[subject]; !ok {
		sp.subjects[subject] = make(map[*Subscriber]struct{})
	}
	sp.subjects[subject][newSubscriber] = struct{}{}
	sp.mu.Unlock()

	sp.wg.Add(1)
	go newSubscriber.dispatchMessages()

	return newSubscriber, nil
}

func (s *Subscriber) dispatchMessages() {
	defer s.spLink.wg.Done()
	for {
		select {
		case <-s.ctx.Done():
			return
		case msg, ok := <-s.messages:
			if !ok {
				return
			}
			s.handler(msg)
		}
	}
}
func (s *Subscriber) Unsubscribe() {
	s.unsubOnce.Do(func() {
		s.cancel()

		s.spLink.mu.Lock()
		if subs, ok := s.spLink.subjects[s.subject]; ok {
			delete(subs, s)
			if len(subs) == 0 {
				delete(s.spLink.subjects, s.subject)
			}
		}
		s.spLink.mu.Unlock()

		s.msgMu.Lock()
		if !s.messagesClosed {
			close(s.messages)
			s.messagesClosed = true
		}
		s.msgMu.Unlock()
	})
}

func (sp *SubPubImplementation) Publish(subject string, msg interface{}) error {
	sp.mu.RLock()
	if sp.closed {
		sp.mu.RUnlock()
		return fmt.Errorf("subpub: system is closed")
	}

	subscribersForSubject, subjectExists := sp.subjects[subject]
	if !subjectExists || len(subscribersForSubject) == 0 {
		sp.mu.RUnlock()
		return nil
	}

	subsCopy := make([]*Subscriber, 0, len(subscribersForSubject))
	for sub := range subscribersForSubject {
		subsCopy = append(subsCopy, sub)
	}
	sp.mu.RUnlock()

	for _, sub := range subsCopy {
		go func(s *Subscriber, messageToDeliver interface{}) {
			select {
			case <-s.ctx.Done():
				return
			case <-sp.ctx.Done():
				return
			default:
			}

			s.msgMu.Lock()
			if s.messagesClosed {
				s.msgMu.Unlock()
				return
			}

			select {
			case s.messages <- messageToDeliver:
			default:
			}
			s.msgMu.Unlock()

		}(sub, msg)
	}
	return nil
}

func (sp *SubPubImplementation) Close(ctx context.Context) error {
	sp.closeOnce.Do(func() {
		sp.mu.Lock()
		sp.closed = true

		allSubsToClose := []*Subscriber{}
		for _, subjectSubs := range sp.subjects {
			for sub := range subjectSubs {
				allSubsToClose = append(allSubsToClose, sub)
			}
		}
		sp.subjects = make(map[string]map[*Subscriber]struct{})
		sp.mu.Unlock()

		sp.cancel()

		for _, sub := range allSubsToClose {
			sub.msgMu.Lock()
			if !sub.messagesClosed {
				close(sub.messages)
				sub.messagesClosed = true
			}
			sub.msgMu.Unlock()
		}
	})
	waitGroupDone := make(chan struct{})
	go func() {
		sp.wg.Wait()
		close(waitGroupDone)
	}()
	select {
	case <-waitGroupDone:
		return nil
	case <-ctx.Done():
		return fmt.Errorf("subpub: close timed out by provided context: %w", ctx.Err())
	}
}
