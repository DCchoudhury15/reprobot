/* Follows a NULL next pointer. */
#include <stddef.h>
#include <stdio.h>

struct node {
	int value;
	struct node *next;
};

__attribute__((noinline)) int second_value(const struct node *n)
{
	return n->next->value;
}

int main(void)
{
	struct node a = {1, NULL};
	printf("%d\n", second_value(&a));
	return 0;
}
