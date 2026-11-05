/* Reads a struct field after the struct was freed. */
#include <stdio.h>
#include <stdlib.h>

struct session {
	int id;
	char user[16];
};

struct session *open_session(int id)
{
	struct session *s = calloc(1, sizeof(*s));
	s->id = id;
	return s;
}

void close_session(struct session *s)
{
	free(s);
}

int session_id(const struct session *s)
{
	return s->id;
}

int main(void)
{
	struct session *s = open_session(7);
	close_session(s);
	printf("%d\n", session_id(s));
	return 0;
}
